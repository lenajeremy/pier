package deploy

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"pier/internal/build"
	"pier/internal/router"
	"pier/internal/store"
)

// gitRepo makes a bare repo for project with one commit of files on branch
// and returns the commit.
func gitRepo(t *testing.T, d *Deployer, project, branch string, files map[string]string) string {
	t.Helper()
	src := t.TempDir()
	run := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run(src, "init", "-q", "-b", branch)
	for name, body := range files {
		os.MkdirAll(filepath.Dir(filepath.Join(src, name)), 0o755)
		os.WriteFile(filepath.Join(src, name), []byte(body), 0o644)
	}
	run(src, "add", ".")
	run(src, "commit", "-q", "-m", "Add search page")
	repo := d.RepoPath(project)
	if _, err := os.Stat(repo); err != nil {
		run(d.Repos, "init", "-q", "--bare", repo)
	}
	run(src, "push", "-q", "-f", repo, branch)
	return run(src, "rev-parse", "HEAD")
}

func newDeployer(t *testing.T, caddy *httptest.Server) *Deployer {
	t.Helper()
	data := t.TempDir()
	s, err := store.Open(filepath.Join(data, "pier.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	d := &Deployer{
		Store: s, Repos: filepath.Join(data, "repos"), Deployments: filepath.Join(data, "deployments"), Tmp: filepath.Join(data, "tmp"),
		Domain: "pier.localhost", ProductionBranch: "main", Runner: build.Local{},
		Router: router.Caddy{Admin: caddy.URL, Listen: []string{":8088"}},
		URL:    func(h string) string { return "http://" + h + ":8088" },
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)), BuildTimeout: time.Minute,
	}
	os.MkdirAll(d.Repos, 0o755)
	return d
}

func fakeCaddy(t *testing.T) (*httptest.Server, *atomic.Value) {
	var last atomic.Value
	last.Store("")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		last.Store(string(b))
	}))
	t.Cleanup(srv.Close)
	return srv, &last
}

func TestDeployStaticToProduction(t *testing.T) {
	caddy, last := fakeCaddy(t)
	d := newDeployer(t, caddy)
	commit := gitRepo(t, d, "blog", "main", map[string]string{"index.html": "<h1>blog</h1>", "about/index.html": "<h1>about</h1>"})

	var log bytes.Buffer
	if err := d.Deploy(context.Background(), "blog", "main", commit, &log); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	out := log.String()
	for _, want := range []string{
		"pier    received main at " + commit[:7],
		"build   static HTML, found in index.html",
		"deploy  kept as ",
		"alias   http://blog.pier.localhost:8088",
		"pier    production ready in ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log is missing %q:\n%s", want, out)
		}
	}

	aliases, _ := d.Store.Aliases(context.Background())
	if len(aliases) != 1 || aliases[0].Host != "blog.pier.localhost" || aliases[0].Kind != store.KindProduction {
		t.Fatalf("aliases: %+v", aliases)
	}
	id := aliases[0].DeploymentID
	if b, err := os.ReadFile(filepath.Join(d.Deployments, id, "about", "index.html")); err != nil || string(b) != "<h1>about</h1>" {
		t.Fatalf("deployment files: %q %v", b, err)
	}
	dep, _ := d.Store.Deployment(context.Background(), id)
	if dep.Status != store.Ready || dep.Files != 2 || dep.Message != "Add search page" {
		t.Fatalf("deployment: %+v", dep)
	}
	cfg := last.Load().(string)
	if !strings.Contains(cfg, `"blog.pier.localhost"`) || !strings.Contains(cfg, `"blog-`+id+`.pier.localhost"`) {
		t.Fatalf("router config is missing an address:\n%s", cfg)
	}
}

func TestDeployBranchAndFailure(t *testing.T) {
	caddy, _ := fakeCaddy(t)
	d := newDeployer(t, caddy)
	ctx := context.Background()

	good := gitRepo(t, d, "blog", "feat-search", map[string]string{"public/index.html": "<h1>search</h1>"})
	var log bytes.Buffer
	if err := d.Deploy(ctx, "blog", "feat-search", good, &log); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if !strings.Contains(log.String(), "alias   http://feat-search--blog.pier.localhost:8088") ||
		!strings.Contains(log.String(), "preview ready in") {
		t.Fatalf("branch log:\n%s", log.String())
	}

	bad := gitRepo(t, d, "blog", "feat-search", map[string]string{"README.md": "nothing to serve"})
	log.Reset()
	if err := d.Deploy(ctx, "blog", "feat-search", bad, &log); err == nil {
		t.Fatal("a push with nothing to serve should fail")
	}
	aliases, _ := d.Store.Aliases(ctx)
	if len(aliases) != 1 {
		t.Fatalf("aliases: %+v", aliases)
	}
	if !strings.Contains(log.String(), "build failed after") ||
		!strings.Contains(log.String(), "feat-search--blog.pier.localhost still serves "+aliases[0].DeploymentID) {
		t.Fatalf("failure log:\n%s", log.String())
	}
}

func TestLineWriter(t *testing.T) {
	var out bytes.Buffer
	w := prefixed(&out, "build   ")
	io.WriteString(w, "added 312 packages\r\n\nin 6s\npartial")
	w.Flush()
	if got := out.String(); got != "build   added 312 packages\nbuild   in 6s\nbuild   partial\n" {
		t.Fatalf("got %q", got)
	}
}
