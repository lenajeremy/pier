package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"pier/internal/monorepo"
	"pier/internal/store"
)

// sourceArchive is a gzipped tar of files. With top set, every entry is in
// that one folder, as in a GitHub source archive.
func sourceArchive(top string, files map[string]string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if top != "" {
		tw.WriteHeader(&tar.Header{Typeflag: tar.TypeXGlobalHeader, Name: "pax_global_header", PAXRecords: map[string]string{"comment": top}})
		tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: top + "/", Mode: 0o755})
		top += "/"
	}
	for _, name := range slices.Sorted(maps.Keys(files)) {
		tw.WriteHeader(&tar.Header{Name: top + name, Mode: 0o644, Size: int64(len(files[name])), Typeflag: tar.TypeReg})
		tw.Write([]byte(files[name]))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

// workspaceFiles is a small pnpm workspace: two apps, a Go service, and a
// package the docs use.
var workspaceFiles = map[string]string{
	"package.json":             `{"private":true}`,
	"pnpm-workspace.yaml":      "packages:\n  - apps/*\n  - packages/*\n",
	"pnpm-lock.yaml":           "lockfileVersion: '9.0'\n",
	"README.md":                "# acme",
	".github/workflows/ci.yml": "on: push",
	"apps/docs/package.json":   `{"name":"docs","dependencies":{"@acme/ui":"workspace:*"}}`,
	"apps/docs/index.html":     "<h1>Docs</h1>",
	"apps/web/package.json":    `{"name":"web"}`,
	"apps/web/next.config.mjs": "export default {}",
	"apps/web/index.html":      "<h1>Web</h1>",
	"packages/ui/package.json": `{"name":"@acme/ui"}`,
	"packages/ui/button.js":    "export {}",
	"services/api/go.mod":      "module api",
	"services/api/main.go":     "package main",
}

// listing reads a folder endpoint's answer.
type listing struct {
	Ref        string            `json:"ref"`
	Branch     string            `json:"branch"`
	Folders    []monorepo.Folder `json:"folders"`
	Truncated  bool              `json:"truncated"`
	Source     string            `json:"source"`
	Deployment string            `json:"deployment"`
	Error      string            `json:"error"`
}

func (e *ghEnv) folders(path string, want int) listing {
	e.t.Helper()
	w := e.call("GET", path, "", ownerHeaders)
	var l listing
	if err := json.Unmarshal(w.Body.Bytes(), &l); w.Code != want || err != nil {
		e.t.Fatalf("GET %s answered %d %s, want %d", path, w.Code, w.Body, want)
	}
	return l
}

// wantWorkspace checks a listing is of workspaceFiles.
func wantWorkspace(t *testing.T, l listing) {
	t.Helper()
	want := []monorepo.Folder{
		{Path: "", Kind: "Workspace"}, {Path: "apps"}, {Path: "apps/docs", Kind: "Node", App: true},
		{Path: "apps/web", Kind: "Next.js", App: true}, {Path: "packages"}, {Path: "packages/ui", Kind: "Node"},
		{Path: "services"}, {Path: "services/api", Kind: "Go", App: true},
	}
	if !slices.Equal(l.Folders, want) || l.Truncated {
		t.Fatalf("listed %+v, truncated %v", l.Folders, l.Truncated)
	}
}

func (e *ghEnv) tree(sha string, files map[string]string) {
	e.hub.mu.Lock()
	defer e.hub.mu.Unlock()
	if e.hub.trees == nil {
		e.hub.trees = map[string][]string{}
	}
	e.hub.trees[sha] = slices.Sorted(maps.Keys(files))
}

func TestRepositoryFolders(t *testing.T) {
	env := newGitHubEnv(t)
	env.tree(commit("a"), workspaceFiles)
	l := env.folders("/api/github/repositories/7/folders?installation=42", 200)
	wantWorkspace(t, l)
	if l.Ref != commit("a") || l.Branch != "main" || l.Source != "" {
		t.Fatalf("listed %s at %s from %q", l.Branch, l.Ref, l.Source)
	}
	if raw := env.call("GET", "/api/github/repositories/7/folders?installation=42", "", ownerHeaders).Body.String(); strings.Contains(raw, "source") {
		t.Fatalf("a repository's listing names a source: %s", raw)
	}

	// The same commit is answered from memory while the owner types.
	env.tree(commit("a"), map[string]string{"index.html": ""})
	wantWorkspace(t, env.folders("/api/github/repositories/7/folders?installation=42&branch=main", 200))

	// Another branch, whose tree GitHub cut short.
	env.hub.mu.Lock()
	env.hub.heads["big"] = commit("b")
	env.hub.clipped = true
	env.hub.mu.Unlock()
	env.tree(commit("b"), map[string]string{"site/index.html": ""})
	if l := env.folders("/api/github/repositories/7/folders?installation=42&branch=big", 200); l.Ref != commit("b") || l.Branch != "big" || !l.Truncated || len(l.Folders) != 2 {
		t.Fatalf("the big branch listed %+v", l)
	}

	if l := env.folders("/api/github/repositories/7/folders?installation=42&branch=gone", 404); l.Error != "owner/site has no branch called gone." {
		t.Fatalf("a missing branch said %q", l.Error)
	}
	env.folders("/api/github/repositories/8/folders?installation=42", 404)
	env.folders("/api/github/repositories/7/folders", 400)
	env.folders("/api/github/repositories/x/folders?installation=42", 400)

	// A commit GitHub has no tree for, then GitHub failing.
	env.hub.mu.Lock()
	env.hub.heads["new"] = commit("c")
	env.hub.mu.Unlock()
	env.folders("/api/github/repositories/7/folders?installation=42&branch=new", 404)
	env.tree(commit("c"), workspaceFiles)
	env.hub.mu.Lock()
	env.hub.fail = func(r *http.Request) int {
		if strings.Contains(r.URL.Path, "/git/trees/") {
			return 500
		}
		return 0
	}
	env.hub.mu.Unlock()
	env.folders("/api/github/repositories/7/folders?installation=42&branch=new", 502)
	env.hub.mu.Lock()
	env.hub.fail = nil
	env.hub.mu.Unlock()

	if code := env.call("GET", "/api/github/repositories/7/folders?installation=42", "", nil).Code; code != 401 {
		t.Fatalf("an anonymous listing answered %d", code)
	}
	if w := env.call("DELETE", "/api/github", "", ownerHeaders); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	env.folders("/api/github/repositories/7/folders?installation=42", 409)
}

func TestProjectFolders(t *testing.T) {
	env := newGitHubEnv(t)
	ctx := context.Background()

	// A project from GitHub lists its repository at the production branch.
	env.tree(commit("a"), workspaceFiles)
	l := env.folders("/api/projects/site/folders", 200)
	wantWorkspace(t, l)
	if l.Source != "github" || l.Ref != commit("a") || l.Branch != "main" {
		t.Fatalf("site listed %s at %s from %s", l.Branch, l.Ref, l.Source)
	}
	if w := env.call("PUT", "/api/projects/site/settings", `{"production_branch":"release","build":{"command":"make","output":"."}}`, ownerHeaders); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if l := env.folders("/api/projects/site/folders", 404); l.Error != "owner/site has no branch called release." {
		t.Fatalf("a production branch GitHub doesn't have said %q", l.Error)
	}

	// A project deployed with git push lists its own repository.
	env.a.Deployer.Repos = t.TempDir()
	if err := env.st.EnsureProject(ctx, "pushed"); err != nil {
		t.Fatal(err)
	}
	sha := bareRepo(t, env.a.Deployer.RepoPath("pushed"), "main", workspaceFiles)
	l = env.folders("/api/projects/pushed/folders", 200)
	wantWorkspace(t, l)
	if l.Source != "git" || l.Ref != sha || l.Branch != "main" {
		t.Fatalf("pushed listed %s at %s from %s", l.Branch, l.Ref, l.Source)
	}

	// One with nothing on its production branch, and nothing uploaded.
	if err := env.st.EnsureProject(ctx, "drafts"); err != nil {
		t.Fatal(err)
	}
	bareRepo(t, env.a.Deployer.RepoPath("drafts"), "draft", workspaceFiles)
	if l := env.folders("/api/projects/drafts/folders", 404); l.Error != "Nothing has been deployed to drafts yet, so there are no folders to show." {
		t.Fatalf("an empty project said %q", l.Error)
	}

	// A project from pier deploy lists its newest upload that is kept.
	if err := env.st.EnsureProject(ctx, "uploaded"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(env.a.Deployer.Sources, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"0a0001", "0a0002", "0a0003"} {
		d := store.Deployment{ID: id, Project: "uploaded", Branch: "main", Commit: commit(string(rune('a' + i))), Status: store.Ready, Target: store.TargetProduction, Source: store.SourceCLI}
		d.CreatedAt = d.CreatedAt.AddDate(2026, 0, i)
		if err := env.st.CreateDeployment(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	// The newest's upload is gone; the one before it is the workspace.
	os.WriteFile(filepath.Join(env.a.Deployer.Sources, "0a0002.tar.gz"), sourceArchive("", workspaceFiles), 0o644)
	os.WriteFile(filepath.Join(env.a.Deployer.Sources, "0a0001.tar.gz"), sourceArchive("", map[string]string{"index.html": ""}), 0o644)
	l = env.folders("/api/projects/uploaded/folders", 200)
	wantWorkspace(t, l)
	if l.Source != "upload" || l.Deployment != "0a0002" || l.Ref != commit("b") || l.Branch != "main" {
		t.Fatalf("uploaded listed %+v", l)
	}

	if l := env.folders("/api/projects/nobody/folders", 404); l.Error != "there's no project called nobody" {
		t.Fatalf("a missing project said %q", l.Error)
	}
	if code := env.call("GET", "/api/projects/site/folders", "", nil).Code; code != 401 {
		t.Fatalf("an anonymous listing answered %d", code)
	}
}

// bareRepo makes a bare repository at repo with one commit of files on
// branch, and returns the commit.
func bareRepo(t *testing.T, repo, branch string, files map[string]string) string {
	t.Helper()
	src := t.TempDir()
	git := func(dir string, args ...string) string {
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
	git(src, "init", "-q", "-b", branch)
	for name, body := range files {
		os.MkdirAll(filepath.Dir(filepath.Join(src, name)), 0o755)
		os.WriteFile(filepath.Join(src, name), []byte(body), 0o644)
	}
	git(src, "add", ".")
	git(src, "commit", "-q", "-m", "Start")
	git(src, "init", "-q", "--bare", repo)
	git(src, "push", "-q", repo, branch)
	return git(src, "rev-parse", "HEAD")
}
