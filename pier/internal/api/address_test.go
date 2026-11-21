package api

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pier/internal/deploy"
	"pier/internal/router"
	"pier/internal/store"
)

// google-prep-os.example.com can become prep.example.com: production moves
// to the new name at once, the old one stops serving, and previews and each
// deployment's own address keep the project's name.
func TestRenamingProductionsAddress(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "pier.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	for _, p := range []string{"blog", "docs"} {
		if err := st.EnsureProject(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sites := ln.Addr().String()
	ln.Close()
	dep := &deploy.Deployer{Store: st, Domain: "pier.localhost", PierHost: "pier.localhost", Deployments: root, ProductionBranch: "main",
		URL: func(host string) string { return "http://" + host }, Router: router.Caddy{Listen: []string{sites}, Storage: filepath.Join(root, "caddy")},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	t.Cleanup(func() { dep.Router.Stop() })
	a := &API{Store: st, Token: "fixture", Deployer: dep, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	api := httptest.NewServer(a.Public())
	defer api.Close()
	call := func(method, path, body string, want int) string {
		t.Helper()
		req, _ := http.NewRequest(method, api.URL+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer fixture")
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != want {
			t.Fatalf("%s %s %s: %d %s", method, path, body, resp.StatusCode, b)
		}
		return string(b)
	}
	visit := func(host string) string {
		t.Helper()
		req, _ := http.NewRequest("GET", "http://"+sites+"/", nil)
		req.Host = host
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	built := func(id string) {
		t.Helper()
		if err := st.CreateDeployment(ctx, store.Deployment{ID: id, Project: "blog", Target: store.TargetProduction, Branch: "main", Status: store.Ready}); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(root, id), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, id, "index.html"), []byte(id), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	built("a00001")
	if err := dep.SetProduction(ctx, "blog", "a00001"); err != nil {
		t.Fatal(err)
	}
	if got := visit("blog.pier.localhost"); got != "a00001" {
		t.Fatalf("production before the rename served %q", got)
	}

	if got := call("PUT", "/api/projects/blog/address", `{"subdomain":"journal"}`, 200); !strings.Contains(got, `"host":"journal.pier.localhost"`) {
		t.Fatalf("rename answered %s", got)
	}
	if got := visit("journal.pier.localhost"); got != "a00001" {
		t.Fatalf("the new address served %q", got)
	}
	if got := visit("blog.pier.localhost"); !strings.Contains(got, "Nothing is deployed") {
		t.Fatalf("the old address still served %q", got)
	}
	if got := visit("blog-a00001.pier.localhost"); got != "a00001" {
		t.Fatalf("the deployment's own address served %q", got)
	}
	for _, path := range []string{"/api/projects/blog/domains", "/api/projects/blog", "/api/projects"} {
		if got := call("GET", path, "", 200); !strings.Contains(got, "journal.pier.localhost") || strings.Contains(got, `"blog.pier.localhost"`) {
			t.Fatalf("%s still names the old address: %s", path, got)
		}
	}

	// The next production deployment goes to the new address too.
	built("a00002")
	if err := dep.SetProduction(ctx, "blog", "a00002"); err != nil {
		t.Fatal(err)
	}
	if got := visit("journal.pier.localhost"); got != "a00002" {
		t.Fatalf("a promote went elsewhere: the new address served %q", got)
	}

	// Names another project goes by, the dashboard's, and malformed ones.
	call("PUT", "/api/projects/docs/address", `{"subdomain":"journal"}`, 409)
	call("PUT", "/api/projects/docs/address", `{"subdomain":"blog"}`, 409)
	call("PUT", "/api/projects/blog/address", `{"subdomain":"docs"}`, 409)
	call("PUT", "/api/projects/blog/address", `{"subdomain":"pier"}`, 400)
	call("PUT", "/api/projects/blog/address", `{"subdomain":"two--dashes"}`, 400)
	call("PUT", "/api/projects/blog/address", `{"subdomain":"docs-c19e77"}`, 400)
	call("PUT", "/api/projects/nope/address", `{"subdomain":"x"}`, 404)
	// A new project can't take a renamed address either.
	call("POST", "/api/projects", `{"name":"journal"}`, 409)

	// A new base domain keeps the name the owner chose.
	if err := st.AlignAliases(ctx, "example.org"); err != nil {
		t.Fatal(err)
	}
	aliases, err := st.Aliases(ctx)
	if err != nil || len(aliases) != 1 || aliases[0].Host != "journal.example.org" {
		t.Fatalf("aliases after a new base domain: %+v, %v", aliases, err)
	}
	if err := st.AlignAliases(ctx, "pier.localhost"); err != nil {
		t.Fatal(err)
	}

	// Empty goes back to the project's name, and frees the old one.
	call("PUT", "/api/projects/blog/address", `{"subdomain":""}`, 200)
	if got := visit("blog.pier.localhost"); got != "a00002" {
		t.Fatalf("going back to the project's name served %q", got)
	}
	call("PUT", "/api/projects/docs/address", `{"subdomain":"journal"}`, 200)
}
