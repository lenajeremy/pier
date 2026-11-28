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
	"crypto/tls"
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

	"pier/internal/alerts"
	"pier/internal/api"
	"pier/internal/apphost"
	"pier/internal/build"
	"pier/internal/challengedns"
	"pier/internal/database"
	"pier/internal/deploy"
	"pier/internal/dnscheck"
	"pier/internal/gitssh"
	"pier/internal/hook"
	"pier/internal/jobs"
	"pier/internal/pgproxy"
	"pier/internal/release"
	"pier/internal/requests"
	"pier/internal/router"
	"pier/internal/secrets"
	"pier/internal/storage"
	"pier/internal/store"
	"pier/internal/upgrade"
	"pier/internal/version"
	"pier/internal/web"
)

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && (!strings.HasPrefix(args[0], "-") || args[0] == "--version" || args[0] == "-v") {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "version", "--version", "-v":
		fmt.Println("pierd " + version.String())
		os.Exit(0)
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
	case "upgrade-apply":
		os.Exit(upgradeApply(args))
	default:
		fmt.Fprintf(os.Stderr, "pierd: unknown command %q (serve, init, reset-password, keys, token, version, upgrade-apply)\n", cmd)
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
	pgAddr := fs.String("postgres", env("PIER_POSTGRES", ":5432"), "where services outside Pier reach databases by their external URLs, over TLS (empty to disable)")
	dnsAddr := fs.String("dns", env("PIER_DNS", ":53"), "where to answer ACME DNS-01 challenges for wildcard HTTPS (empty to disable)")
	builder := fs.String("builder", env("PIER_BUILDER", "railpack-preview"), "railpack-preview, docker, or local (host builds without isolation)")
	image := fs.String("image", env("PIER_IMAGE", build.DefaultNodeImage), "the image builds run in")
	prod := fs.String("production-branch", env("PIER_PRODUCTION_BRANCH", "main"), "the branch that deploys to production")
	sleepDefault := 30 * time.Minute
	if v, err := time.ParseDuration(env("PIER_SLEEP_AFTER", "")); err == nil {
		sleepDefault = v
	}
	sleepAfter := fs.Duration("sleep-after", sleepDefault, "how long a preview's server runs without a visit before it sleeps until the next one (0 keeps previews awake)")
	// For trying updates without GitHub or systemd: where to ask which
	// release is the newest, and the unit file that says the update helper
	// is installed. Neither changes where a release is downloaded from.
	releaseAPI := fs.String("release-api", release.LatestAPI, "where to ask for the latest release; for testing only")
	upgradeUnit := fs.String("upgrade-unit", upgrade.HelperUnit, "the unit file that shows the update helper is installed; for testing only")
	fs.Parse(args)

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	log.Info("starting pierd", "version", version.String())
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
	caddyCfg := router.Caddy{Admin: *caddyAdmin, Storage: filepath.Join(*data, "caddy"), Listen: []string{*httpAddr}, HTTPS: *https, BaseDomain: *domain, AccessLog: paths.accessSock}
	// External URLs are on for a server with HTTPS on a real domain, or
	// when -postgres is given. No site asks for db.<domain>'s certificate,
	// so the router asks for it.
	realDomain := *https && *caddyAdmin == "" && *domain != "" && *domain != "localhost" && !strings.HasSuffix(*domain, ".localhost")
	externalDB := *pgAddr != "" && (realDomain || flagSet(fs, "postgres"))
	if externalDB && *https && *caddyAdmin == "" {
		caddyCfg.Certify = []string{"db." + *domain}
	}
	var wild wildcard
	var challengeNS string
	// probeWildcard asks DNS once whether the wildcard certificate can be
	// issued, and reports whether the router's config has to change.
	probeWildcard := func(ctx context.Context, timeout time.Duration) bool {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		delegated := dnscheck.ChallengeAuthority(ctx, *domain, challengeNS)
		return wild.record(delegated, !delegated && caddyCfg.HasWildcardCertificate())
	}
	if *https && *caddyAdmin == "" && *dnsAddr != "" && *domain != "" && *domain != "localhost" && !strings.HasSuffix(*domain, ".localhost") {
		challengeNS = *address
		if net.ParseIP(challengeNS) != nil || challengeNS == *domain {
			challengeNS = "pier." + *domain
		}
		if srv, bound, err := startChallengeDNS(*domain, challengeNS, *dnsAddr); err != nil {
			log.Warn("ACME DNS responder unavailable; using per-host HTTPS certificates", "addr", *dnsAddr, "err", err)
			challengeNS = ""
		} else {
			defer srv.Close()
			log.Info("ACME DNS responder listening", "zone", "_acme-challenge."+*domain, "addr", bound)
			probeWildcard(setup, 4*time.Second)
			caddyCfg.WildcardReady = wild.ready.Load
		}
	}
	// Caddy sends visits to an application that is asleep here, and pierd
	// wakes it. Only Caddy should reach it, so it listens on loopback.
	wakeLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Error("listen for visits to sleeping applications", "err", err)
		return 1
	}
	defer wakeLn.Close()
	dep := &deploy.Deployer{
		Waker: wakeLn.Addr().String(), SleepAfter: *sleepAfter,
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

	// Alerts go out in the background, so a deploy never waits for one.
	notifier := &alerts.Notifier{Store: st, Box: box, Log: log, Dashboard: dep.URL(*address), Server: *address}
	dep.Alerts = notifier
	// Each project's Postgres database, its previews' databases, and their
	// backups, kept in the data folder.
	dbs := &database.Manager{Store: st, Box: box, Host: dep.Apps, Backups: filepath.Join(*data, "backups"), Log: log, Alerts: notifier}
	dep.Databases = dbs
	// Each project's buckets, on the owner's Cloudflare R2 once it's
	// connected in Integrations.
	buckets := &storage.Manager{Store: st, Box: box, Log: log, Domain: *domain,
		// Stand-ins for Cloudflare's API and R2, for tests only.
		Base: os.Getenv("PIER_TEST_CLOUDFLARE_API"), Endpoint: os.Getenv("PIER_TEST_R2_ENDPOINT")}
	dep.Storage = buckets

	self, err := os.Executable()
	if err != nil {
		log.Error("find own binary", "err", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go notifier.Run(ctx)
	// Which release is the newest, asked of GitHub every few hours.
	releases := &release.Checker{API: *releaseAPI, Log: log}
	go releases.Run(ctx)

	if challengeNS != "" {
		go func() {
			t := time.NewTicker(30 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					if probeWildcard(ctx, 8*time.Second) {
						log.Info("wildcard certificate delegation changed", "ready", wild.ready.Load())
						if err := dep.Sync(ctx); err != nil && ctx.Err() == nil {
							log.Warn("sync router after delegation change", "err", err)
						}
					}
				}
			}
		}()
	}

	deployment := deploymentOf(st)
	reqs, err := requests.Open(paths.requestsDB, func(id string) string { p, _ := deployment(id); return p }, log)
	if err != nil {
		log.Error("open the requests database", "err", err)
		return 1
	}
	defer reqs.Close()
	reqs.Target = func(id string) string { _, t := deployment(id); return t }
	dep.Visited = reqs.LastSeen
	waker := &http.Server{Handler: dep.WakeHandler(), ReadHeaderTimeout: 5 * time.Second}
	go waker.Serve(wakeLn)
	defer waker.Close()
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
	// After a reboot Docker may have started applications that nothing
	// serves any more; the addresses above are up before any is stopped.
	dep.StopSupersededLater("")

	// Scheduled jobs: a command runs in a fresh container from production's
	// image, and a path is asked for through the router, as a visit would be.
	scheduler := &jobs.Scheduler{
		Store: st, Deployer: dep, Alerts: notifier, Log: log, Logs: filepath.Join(paths.logs, "jobs"),
		Commands: dep.Apps, Sites: dialable(*httpAddr), HTTPS: *https,
	}
	a := &api.API{StartedAt: time.Now(), Store: st, Deployer: dep, Token: tok, Log: log, Web: web.Handler(), Requests: reqs, SiteListener: dialable(*httpAddr), Alerts: notifier, Jobs: scheduler,
		Databases: dbs, Storage: buckets, Releases: releases, Data: abs, UpgradeUnit: *upgradeUnit}
	if err := a.ResumeCleanup(ctx); err != nil {
		log.Warn("resume preview cleanup", "err", err)
	}
	// A build cut short by the stop records that on its way out, so the
	// database stays open until the queue returns, for a while at most.
	githubDone := make(chan struct{})
	go func() { defer close(githubDone); a.RunGitHub(ctx) }()
	defer func() {
		stop()
		select {
		case <-githubDone:
		case <-time.After(30 * time.Second):
			log.Warn("the GitHub queue didn't stop within 30 s; closing the database anyway")
		}
	}()
	// A run cut short records that on its way out, so the database stays
	// open until the scheduler returns.
	jobsDone := make(chan struct{})
	go func() { defer close(jobsDone); scheduler.Run(ctx) }()
	defer func() { stop(); <-jobsDone }()
	go a.SampleServer(ctx)
	go dbs.Run(ctx)
	if externalDB {
		if ln, err := net.Listen("tcp", *pgAddr); err != nil {
			log.Warn("external database URLs are off: can't listen", "addr", *pgAddr, "err", err)
		} else {
			defer ln.Close()
			host := "db." + *domain
			dbs.External = host + ":" + portOf(*pgAddr)
			proxy := &pgproxy.Proxy{TLS: &tls.Config{GetCertificate: certificates(caddyCfg, host, log)}, Route: dbs.Route, Log: log}
			go proxy.Serve(ln)
			log.Info("external database URLs", "host", dbs.External)
		}
	}
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
	go dep.SleepIdle(ctx, sweepEvery(*sleepAfter))
	go dep.WatchHealth(ctx, deploy.HealthEvery)
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
				if err := dep.RetryStops(ctx); err != nil {
					log.Warn("stop superseded applications", "err", err)
				}
				// Activity older than a year goes, so the feed stays quick to read.
				if _, err := st.PruneEvents(ctx); err != nil {
					log.Warn("prune activity", "err", err)
				}
			}
		}
	}()

	fmt.Fprintf(os.Stderr, `
pierd %s is running
  dashboard  %s   (or http://%s)
  push       git remote add pier ssh://git@localhost%s/<project>
  sites      %s
  api        http://%s   (token: pierd token -data %s)
  builds     %s

`, version.String(), dep.URL(*address), *apiAddr, *sshAddr, dep.URL("<project>."+*domain), *apiAddr, *data, runner.Name())
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
	// Alert deliveries stop with ctx and stay waiting in the database for
	// the next start; let each finish noting where it got to first.
	notifier.Wait()
	os.Remove(paths.socket)
	return 0
}

// sweepEvery is how often pierd looks for applications to put to sleep: a
// quarter of the idle time, between five seconds and a minute.
func sweepEvery(sleepAfter time.Duration) time.Duration {
	return min(max(sleepAfter/4, 5*time.Second), time.Minute)
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
// deploymentOf looks up a deployment's project and target once and
// remembers them: the request log asks for every request.
func deploymentOf(st *store.Store) func(string) (string, string) {
	type known struct{ project, target string }
	var mu sync.Mutex
	seen := map[string]known{}
	return func(id string) (string, string) {
		mu.Lock()
		k, ok := seen[id]
		mu.Unlock()
		if ok {
			return k.project, k.target
		}
		d, err := st.Deployment(context.Background(), id)
		if err != nil {
			return "", ""
		}
		mu.Lock()
		seen[id] = known{d.Project, d.Target}
		mu.Unlock()
		return d.Project, d.Target
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

// wildcard decides whether the router asks for and serves the wildcard
// certificate. Taking its policy out of the config makes Caddy drop the
// certificate, and every preview then asks Let's Encrypt for one of its own
// until the weekly limit runs out. One failed DNS probe must not do that: it
// takes wildcardFailures in a row, and no usable certificate in storage.
type wildcard struct {
	ready    atomic.Bool
	failures int // failed probes in a row; only the prober uses it
}

const wildcardFailures = 5

// record takes one probe's answers: whether DNS showed the challenge zone
// delegated to this server, and whether a wildcard certificate that hasn't
// expired is stored. It reports whether the router's config has to change.
func (w *wildcard) record(delegated, stored bool) (changed bool) {
	if delegated {
		w.failures = 0
	} else if w.failures < wildcardFailures {
		w.failures++
	}
	ready := delegated || stored || (w.ready.Load() && w.failures < wildcardFailures)
	return w.ready.Swap(ready) != ready
}

// startChallengeDNS binds Pier's ACME DNS-01 listener. When the default
// address (":53") collides with a local stub resolver such as Ubuntu's
// systemd-resolved on 127.0.0.53:53, it binds each of the machine's own
// addresses instead, and works if it gets at least one.
func startChallengeDNS(domain, ns, addr string) (*challengedns.Server, string, error) {
	srv, err := challengedns.Start(domain, ns, addr)
	if err == nil {
		return srv, addr, nil
	}
	host, port, splitErr := net.SplitHostPort(addr)
	if splitErr != nil || host != "" {
		return nil, "", err
	}
	local, _ := net.InterfaceAddrs()
	srv, startErr := challengedns.Start(domain, ns)
	if startErr != nil {
		return nil, "", err
	}
	var bound []string
	for _, a := range challengeAddrs(local, port) {
		if srv.Listen(a) == nil {
			bound = append(bound, a)
		}
	}
	if len(bound) == 0 {
		srv.Close()
		return nil, "", err
	}
	return srv, strings.Join(bound, ", "), nil
}

// challengeAddrs lists where the ACME DNS responder listens when it can't
// have its port on every address at once: each address of the machine's
// interfaces, IPv4 and IPv6. The public IP the owner typed isn't one of
// them behind NAT (EC2, Lightsail, GCP, Azure, Oracle), where binding it
// fails and the wildcard certificate would never be issued. Loopback is left
// to the local resolver, and nothing outside reaches a link-local address.
func challengeAddrs(local []net.Addr, port string) []string {
	var out []string
	for _, a := range local {
		var ip net.IP
		switch a := a.(type) {
		case *net.IPNet:
			ip = a.IP
		case *net.IPAddr:
			ip = a.IP
		}
		if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() {
			continue
		}
		out = append(out, net.JoinHostPort(ip.String(), port))
	}
	return out
}

// certificates hands the public Postgres port the certificate embedded
// Caddy keeps for host, or the base domain's wildcard, read from its
// storage at most every ten minutes, so a renewal is picked up.
func certificates(c router.Caddy, host string, log *slog.Logger) func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	var mu sync.Mutex
	var cert *tls.Certificate
	var at time.Time
	return func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		mu.Lock()
		defer mu.Unlock()
		if cert != nil && time.Since(at) < 10*time.Minute {
			return cert, nil
		}
		next, err := c.Certificate(host)
		if err != nil {
			if cert != nil {
				return cert, nil
			}
			log.Warn("no certificate for external database URLs yet", "host", host, "err", err)
			return nil, err
		}
		cert, at = next, time.Now()
		return cert, nil
	}
}
