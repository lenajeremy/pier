// Command pierd is Pier's daemon. It accepts git pushes over SSH, builds
// them, keeps every deployment, and drives Caddy so each one has an address.
//
//	pierd [serve]          run the daemon
//	pierd keys add FILE    let a public key push (- reads stdin)
//	pierd token            print the API token for `pier login`
//	pierd hook             run by git after a push; not for people
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"

	"pier/internal/api"
	"pier/internal/apphost"
	"pier/internal/build"
	"pier/internal/challengedns"
	"pier/internal/deploy"
	"pier/internal/dnscheck"
	"pier/internal/gitssh"
	"pier/internal/hook"
	"pier/internal/requests"
	"pier/internal/router"
	"pier/internal/secrets"
	"pier/internal/store"
	"pier/internal/web"
)

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "serve":
		os.Exit(serve(args))
	case "init":
		os.Exit(initCmd(args))
	case "reset-password":
		os.Exit(resetPassword(args))
	case "hook":
		os.Exit(hook.Run(os.Stdin, os.Stdout))
	case "keys":
		os.Exit(keys(args))
	case "token":
		os.Exit(token(args))
	default:
		fmt.Fprintf(os.Stderr, "pierd: unknown command %q (serve, init, reset-password, keys, token)\n", cmd)
		os.Exit(2)
	}
}

// signalContext is cancelled by Ctrl-C or SIGTERM.
func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func dataFlag(fs *flag.FlagSet) *string {
	return fs.String("data", env("PIER_DATA", "/var/lib/pier"), "where pierd keeps its database, repositories and deployments")
}

func serve(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	data := dataFlag(fs)
	domain := fs.String("domain", env("PIER_DOMAIN", "pier.localhost"), "the domain every address goes under (pierd init's base domain wins)")
	address := fs.String("address", env("PIER_ADDRESS", ""), "where the dashboard is reached (pierd init's address wins; default: the domain itself)")
	sshAddr := fs.String("ssh", env("PIER_SSH", ":2222"), "where to accept git pushes")
	apiAddr := fs.String("api", env("PIER_API", "127.0.0.1:7777"), "where to serve the API")
	caddyAdmin := fs.String("caddy-admin", env("PIER_CADDY_ADMIN", ""), "use an external Caddy admin API instead of the embedded router")
	httpAddr := fs.String("http", env("PIER_HTTP", ":8088"), "where Caddy serves the sites")
	https := fs.Bool("https", env("PIER_HTTPS", "") == "1", "serve sites over HTTPS with certificates (production)")
	dnsAddr := fs.String("dns", env("PIER_DNS", ":53"), "where to answer ACME DNS-01 challenges for wildcard HTTPS (empty to disable)")
	builder := fs.String("builder", env("PIER_BUILDER", "railpack-preview"), "railpack-preview, docker, or local (host builds without isolation)")
	image := fs.String("image", env("PIER_IMAGE", build.DefaultNodeImage), "the image builds run in")
	prod := fs.String("production-branch", env("PIER_PRODUCTION_BRANCH", "main"), "the branch that deploys to production")
	fs.Parse(args)

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	// git runs the hook from inside the repository, and Caddy from its own
	// folder, so every path pierd hands out must be absolute.
	abs, err := filepath.Abs(*data)
	if err != nil {
		log.Error("data folder", "err", err)
		return 1
	}
	*data = abs
	paths := layout(*data)
	if err := os.MkdirAll(*data, 0o700); err != nil {
		log.Error("create data folder", "err", err)
		return 1
	}
	for _, dir := range []string{paths.repos, paths.deployments, paths.logs, paths.sources, paths.tmp} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Error("create data folder", "err", err)
			return 1
		}
	}
	st, err := store.Open(paths.db)
	if err != nil {
		log.Error("open database", "err", err)
		return 1
	}
	defer st.Close()
	box, err := secrets.Load(paths.secretKey)
	if err != nil {
		log.Error("secret key", "err", err)
		return 1
	}
	tok, err := loadOrCreateToken(paths.token)
	if err != nil {
		log.Error("api token", "err", err)
		return 1
	}
	if _, err := os.Stat(paths.keys); errors.Is(err, os.ErrNotExist) {
		os.WriteFile(paths.keys, nil, 0o600)
	}

	// What pierd init chose wins over the development defaults.
	setup := context.Background()
	if base, _ := st.Setting(setup, store.SettingBaseDomain); base != "" && !flagSet(fs, "domain") {
		*domain = base
	}
	if addr, _ := st.Setting(setup, store.SettingAddress); addr != "" && !flagSet(fs, "address") {
		*address = addr
	}
	if *address == "" {
		*address = *domain
	}
	if err := st.AlignAliases(setup, *domain); err != nil {
		log.Error("restore project addresses", "err", err)
		return 1
	}
	owners, err := st.UserCount(setup)
	if err != nil {
		log.Error("read accounts", "err", err)
		return 1
	}

	var runner build.Runner = build.Docker{Image: *image, CacheVolume: "pier-build-cache"}
	var preview *build.Railpack
	switch *builder {
	case "railpack-preview":
		preview = &build.Railpack{Image: *image}
	case "docker":
	case "local":
		runner = build.Local{}
	default:
		log.Error("unknown builder", "builder", *builder)
		return 1
	}
	scheme, port := "http", portOf(*httpAddr)
	if *https {
		scheme = "https"
	}
	var wildcardReady atomic.Bool
	var challengeNS string
	if *https && *caddyAdmin == "" && *dnsAddr != "" && *domain != "" && *domain != "localhost" && !strings.HasSuffix(*domain, ".localhost") {
		challengeNS = *address
		if net.ParseIP(challengeNS) != nil || challengeNS == *domain {
			challengeNS = "pier." + *domain
		}
		pubIP, _ := st.Setting(setup, store.SettingPublicIP)
		if srv, bound, err := startChallengeDNS(*domain, challengeNS, *dnsAddr, pubIP); err != nil {
			log.Warn("ACME DNS responder unavailable; using per-host HTTPS certificates", "addr", *dnsAddr, "err", err)
			challengeNS = ""
		} else {
			defer srv.Close()
			log.Info("ACME DNS responder listening", "zone", "_acme-challenge."+*domain, "addr", bound)
			probeCtx, cancel := context.WithTimeout(setup, 4*time.Second)
			if dnscheck.ChallengeAuthority(probeCtx, *domain, challengeNS) {
				wildcardReady.Store(true)
			}
			cancel()
		}
	}
	caddyCfg := router.Caddy{Admin: *caddyAdmin, Storage: filepath.Join(*data, "caddy"), Listen: []string{*httpAddr}, HTTPS: *https, BaseDomain: *domain, AccessLog: paths.accessSock}
	if challengeNS != "" {
		caddyCfg.WildcardReady = wildcardReady.Load
	}
	dep := &deploy.Deployer{
		Store: st, Repos: paths.repos, Deployments: paths.deployments, Logs: paths.logs, Sources: paths.sources, Tmp: paths.tmp,
		Secrets: box,
		Domain:  *domain, ProductionBranch: *prod, Runner: runner, PreviewRailpack: preview, Apps: apphost.New(*data), Log: log, BuildTimeout: 15 * time.Minute,
		PierHost: *address, PierUpstream: dialable(*apiAddr),
		Router: caddyCfg,
		URL: func(host string) string {
			if port == "" || (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
				return scheme + "://" + host
			}
			return scheme + "://" + host + ":" + port
		},
	}

	self, err := os.Executable()
	if err != nil {
		log.Error("find own binary", "err", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if challengeNS != "" {
		go func() {
			t := time.NewTicker(30 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
					ready := dnscheck.ChallengeAuthority(probeCtx, *domain, challengeNS)
					cancel()
					if ready != wildcardReady.Swap(ready) {
						log.Info("wildcard certificate delegation changed", "ready", ready)
						if err := dep.Sync(ctx); err != nil && ctx.Err() == nil {
							log.Warn("sync router after delegation change", "err", err)
						}
					}
				}
			}
		}()
	}

	reqs, err := requests.Open(paths.requestsDB, projectOf(st), log)
	if err != nil {
		log.Error("open the requests database", "err", err)
		return 1
	}
	defer reqs.Close()
	go func() {
		if err := reqs.Listen(ctx, paths.accessSock); err != nil {
			log.Error("request log socket", "err", err)
		}
	}()

	if err := st.InterruptAppBuilds(ctx); err != nil {
		log.Error("recover interrupted application builds", "err", err)
		return 1
	}
	if err := dep.Sync(ctx); err != nil {
		if *caddyAdmin == "" {
			log.Error("start embedded Caddy", "err", err)
			return 1
		}
		log.Warn("router not reachable yet; pierd will keep trying", "err", err)
	}
	defer func() {
		if err := dep.Router.Stop(); err != nil {
			log.Error("stop router", "err", err)
		}
	}()

	a := &api.API{StartedAt: time.Now(), Store: st, Deployer: dep, Token: tok, Log: log, Web: web.Handler(), Requests: reqs, SiteListener: dialable(*httpAddr)}
	if err := a.ResumeCleanup(ctx); err != nil {
		log.Warn("resume preview cleanup", "err", err)
	}
	go a.RunGitHub(ctx)
	go a.SampleServer(ctx)
	domainsDone := make(chan struct{})
	go func() { defer close(domainsDone); a.RunDomainChecks(ctx) }()
	defer func() { stop(); <-domainsDone }()
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			if err := st.DeleteExpiredSessions(ctx); err != nil && ctx.Err() == nil {
				log.Warn("clear expired sessions", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()

	os.Remove(paths.socket)
	sock, err := net.Listen("unix", paths.socket)
	if err != nil {
		log.Error("listen on socket", "err", err)
		return 1
	}
	os.Chmod(paths.socket, 0o600)
	internal := &http.Server{Handler: a.Internal(), ReadHeaderTimeout: 5 * time.Second}
	go internal.Serve(sock)

	public := &http.Server{Addr: *apiAddr, Handler: a.Public(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := public.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("api", "err", err)
			stop()
		}
	}()

	git := &gitssh.Server{
		Addr: *sshAddr, HostKeyPath: paths.hostKey, AuthorizedKeys: paths.keys,
		Repo: dep.RepoPath, Hook: gitssh.HookScript(self), Log: log,
		Env:          []string{"PIER_SOCKET=" + paths.socket},
		ShellMessage: fmt.Sprintf("Pier only accepts git pushes here.\nAdd it as a remote:  git remote add pier ssh://git@<this server>%s/<project>\n", *sshAddr),
	}
	go func() {
		if err := git.Serve(ctx); err != nil {
			log.Error("ssh", "err", err)
			stop()
		}
	}()

	go dep.KeepSynced(ctx, 5*time.Second)
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := dep.CleanupApps(ctx); err != nil {
					log.Warn("application cleanup", "err", err)
				}
			}
		}
	}()

	fmt.Fprintf(os.Stderr, `
pierd is running
  dashboard  %s   (or http://%s)
  push       git remote add pier ssh://git@localhost%s/<project>
  sites      %s
  api        http://%s   (token: pierd token -data %s)
  builds     %s

`, dep.URL(*address), *apiAddr, *sshAddr, dep.URL("<project>."+*domain), *apiAddr, *data, runner.Name())
	if owners == 0 {
		fmt.Fprintf(os.Stderr, "Nobody can sign in yet. Create the owner's account:  pierd init -data %s\n\n", *data)
	}
	if fi, _ := os.Stat(paths.keys); fi != nil && fi.Size() == 0 {
		fmt.Fprintf(os.Stderr, "No SSH keys can push yet. Add yours:  pierd keys add -data %s ~/.ssh/id_ed25519.pub\n\n", *data)
	}

	<-ctx.Done()
	shut, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	public.Shutdown(shut)
	internal.Shutdown(shut)
	os.Remove(paths.socket)
	return 0
}

type paths struct {
	db, repos, deployments, logs, sources, tmp, token, keys, hostKey, socket, secretKey string
	requestsDB, accessSock                                                              string
}

func layout(data string) paths {
	j := func(p string) string { return filepath.Join(data, p) }
	return paths{
		db: j("pier.db"), repos: j("repos"), deployments: j("deployments"), logs: j("logs"), sources: j("sources"), tmp: j("tmp"),
		secretKey: j("secret.key"), requestsDB: j("requests.db"), accessSock: j("access.sock"),
		token: j("api.token"), keys: j("authorized_keys"), hostKey: j("ssh_host_ed25519_key"), socket: j("pierd.sock"),
	}
}

// projectOf finds which project a deployment belongs to, remembering the
// answers: the request log asks for every request.
func projectOf(st *store.Store) func(string) string {
	var mu sync.Mutex
	known := map[string]string{}
	return func(id string) string {
		mu.Lock()
		p, ok := known[id]
		mu.Unlock()
		if ok {
			return p
		}
		d, err := st.Deployment(context.Background(), id)
		if err != nil {
			return ""
		}
		mu.Lock()
		known[id] = d.Project
		mu.Unlock()
		return d.Project
	}
}

// flagSet reports whether a flag was given on the command line or through
// its environment variable, rather than left at its default.
func flagSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	if set {
		return true
	}
	return os.Getenv("PIER_"+strings.ToUpper(strings.ReplaceAll(name, "-", "_"))) != ""
}

// dialable turns a listen address like ":7777" into one the router can dial.
func dialable(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

func portOf(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	return port
}

func loadOrCreateToken(path string) (string, error) {
	if b, err := os.ReadFile(path); err == nil {
		return strings.TrimSpace(string(b)), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	t := "pier_" + hex.EncodeToString(b)
	return t, os.WriteFile(path, []byte(t+"\n"), 0o600)
}

func keys(args []string) int {
	fs := flag.NewFlagSet("keys", flag.ExitOnError)
	data := dataFlag(fs)
	if len(args) == 0 || args[0] != "add" {
		fmt.Fprintln(os.Stderr, "usage: pierd keys add [-data DIR] FILE   (FILE is a public key, - for stdin)")
		return 2
	}
	fs.Parse(args[1:])
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: pierd keys add [-data DIR] FILE")
		return 2
	}
	var raw []byte
	var err error
	if fs.Arg(0) == "-" {
		raw, err = io.ReadAll(os.Stdin)
	} else {
		raw, err = os.ReadFile(fs.Arg(0))
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "pierd:", err)
		return 1
	}
	key, comment, _, _, err := ssh.ParseAuthorizedKey(raw)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pierd: that isn't an SSH public key (it should start with ssh-ed25519 or ssh-rsa)")
		return 1
	}
	os.MkdirAll(*data, 0o755)
	path := layout(*data).keys
	existing, _ := os.ReadFile(path)
	line := bytes.TrimSpace(ssh.MarshalAuthorizedKey(key))
	if bytes.Contains(existing, line) {
		fmt.Printf("%s can already push.\n", ssh.FingerprintSHA256(key))
		return 0
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pierd:", err)
		return 1
	}
	defer f.Close()
	if comment != "" {
		line = append(line, ' ')
		line = append(line, comment...)
	}
	f.Write(append(line, '\n'))
	fmt.Printf("Added %s. It can push now.\n", ssh.FingerprintSHA256(key))
	return 0
}

func token(args []string) int {
	fs := flag.NewFlagSet("token", flag.ExitOnError)
	data := dataFlag(fs)
	fs.Parse(args)
	t, err := loadOrCreateToken(layout(*data).token)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pierd:", err)
		return 1
	}
	fmt.Println(t)
	return 0
}

// startChallengeDNS binds Pier's ACME DNS-01 listener. When the default wildcard
// address (e.g. ":53") collides with a local stub resolver such as Ubuntu's
// systemd-resolved on 127.0.0.53:53, it falls back to binding the server's
// public IP on the same port.
func startChallengeDNS(domain, ns, addr, publicIP string) (*challengedns.Server, string, error) {
	srv, err := challengedns.Start(domain, ns, addr)
	if err == nil {
		return srv, addr, nil
	}
	host, port, splitErr := net.SplitHostPort(addr)
	if splitErr != nil || host != "" {
		return nil, "", err
	}
	ip := publicIP
	if net.ParseIP(ip) == nil {
		if detected, derr := dnscheck.PublicIP(); derr == nil && net.ParseIP(detected) != nil {
			ip = detected
		}
	}
	if net.ParseIP(ip) == nil {
		return nil, "", err
	}
	fallback := net.JoinHostPort(ip, port)
	if srv2, err2 := challengedns.Start(domain, ns, fallback); err2 == nil {
		return srv2, fallback, nil
	}
	return nil, "", err
}
