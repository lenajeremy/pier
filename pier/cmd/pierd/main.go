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
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"

	"pier/internal/api"
	"pier/internal/build"
	"pier/internal/deploy"
	"pier/internal/gitssh"
	"pier/internal/hook"
	"pier/internal/router"
	"pier/internal/store"
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
	case "hook":
		os.Exit(hook.Run(os.Stdin, os.Stdout))
	case "keys":
		os.Exit(keys(args))
	case "token":
		os.Exit(token(args))
	default:
		fmt.Fprintf(os.Stderr, "pierd: unknown command %q (serve, keys, token)\n", cmd)
		os.Exit(2)
	}
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
	domain := fs.String("domain", env("PIER_DOMAIN", "pier.localhost"), "the domain every address goes under")
	sshAddr := fs.String("ssh", env("PIER_SSH", ":2222"), "where to accept git pushes")
	apiAddr := fs.String("api", env("PIER_API", "127.0.0.1:7777"), "where to serve the API")
	caddyAdmin := fs.String("caddy-admin", env("PIER_CADDY_ADMIN", "http://127.0.0.1:2019"), "Caddy's admin API")
	httpAddr := fs.String("http", env("PIER_HTTP", ":8088"), "where Caddy serves the sites")
	https := fs.Bool("https", env("PIER_HTTPS", "") == "1", "serve sites over HTTPS with certificates (production)")
	builder := fs.String("builder", env("PIER_BUILDER", "docker"), "docker, or local to build on the host without isolation")
	image := fs.String("image", env("PIER_IMAGE", "node:22-alpine"), "the image builds run in")
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
	for _, dir := range []string{paths.repos, paths.deployments, paths.tmp} {
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
	tok, err := loadOrCreateToken(paths.token)
	if err != nil {
		log.Error("api token", "err", err)
		return 1
	}
	if _, err := os.Stat(paths.keys); errors.Is(err, os.ErrNotExist) {
		os.WriteFile(paths.keys, nil, 0o600)
	}

	var runner build.Runner = build.Docker{Image: *image, CacheVolume: "pier-build-cache"}
	if *builder == "local" {
		runner = build.Local{}
	}
	scheme, port := "http", portOf(*httpAddr)
	if *https {
		scheme = "https"
	}
	dep := &deploy.Deployer{
		Store: st, Repos: paths.repos, Deployments: paths.deployments, Tmp: paths.tmp,
		Domain: *domain, ProductionBranch: *prod, Runner: runner, Log: log, BuildTimeout: 15 * time.Minute,
		Router: router.Caddy{Admin: *caddyAdmin, Listen: []string{*httpAddr}, HTTPS: *https},
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

	a := &api.API{Store: st, Deployer: dep, Token: tok, Log: log}

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

	if err := dep.Sync(ctx); err != nil {
		log.Warn("router not reachable yet; pierd will keep trying", "err", err)
	}
	go dep.KeepSynced(ctx, 5*time.Second)

	fmt.Fprintf(os.Stderr, `
pierd is running
  push     git remote add pier ssh://git@localhost%s/<project>
  sites    %s
  api      http://%s   (token: pierd token -data %s)
  builds   %s

`, *sshAddr, dep.URL("<project>."+*domain), *apiAddr, *data, runner.Name())
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

type paths struct{ db, repos, deployments, tmp, token, keys, hostKey, socket string }

func layout(data string) paths {
	j := func(p string) string { return filepath.Join(data, p) }
	return paths{
		db: j("pier.db"), repos: j("repos"), deployments: j("deployments"), tmp: j("tmp"),
		token: j("api.token"), keys: j("authorized_keys"), hostKey: j("ssh_host_ed25519_key"), socket: j("pierd.sock"),
	}
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
