package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"pier/internal/apphost"
	"pier/internal/apphost/dockertest"
	"pier/internal/deploy"
	"pier/internal/router"
	"pier/internal/store"
)

// A rollback to a server deployment that was stopped starts it, then stops
// what the move left superseded. One that won't start says why, and
// production stays where it is.
func TestRollbackToAStoppedServerDeployment(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "pier.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.EnsureProject(ctx, "shop"); err != nil {
		t.Fatal(err)
	}
	caddy := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { io.Copy(io.Discard, r.Body) }))
	defer caddy.Close()
	engine := dockertest.Start(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	dep := &deploy.Deployer{
		Store: st, Domain: "pier.localhost", Deployments: root, ProductionBranch: "main", Apps: apphost.New(root), Log: log,
		URL: func(host string) string { return "http://" + host }, Router: router.Caddy{Admin: caddy.URL, Listen: []string{":8088"}},
	}
	a := &API{Store: st, Token: "fixture", Deployer: dep, Log: log}
	api := httptest.NewServer(a.Public())
	defer api.Close()

	for i, c := range []dockertest.Container{{Crashes: true}, {}, {Running: true}, {Running: true}} {
		id := "a0000" + string(rune('0'+i))
		d := store.Deployment{ID: id, Project: "shop", Branch: "main", Target: store.TargetProduction, Status: store.Building, CreatedAt: time.Now().Add(time.Duration(i-60) * time.Minute)}
		if err := st.CreateDeployment(ctx, d); err != nil {
			t.Fatal(err)
		}
		if err := st.TrackApp(ctx, id, "pier-app-"+id); err != nil {
			t.Fatal(err)
		}
		d.Status = store.Ready
		if err := st.FinishDeployment(ctx, d); err != nil {
			t.Fatal(err)
		}
		c.Name, c.Labels, c.Policy = "pier-app-"+id, dep.Apps.Labels(), "unless-stopped"
		engine.Add(c)
	}
	if err := st.SetAlias(ctx, store.Alias{Host: "shop.pier.localhost", Project: "shop", Kind: store.KindProduction, DeploymentID: "a00003"}); err != nil {
		t.Fatal(err)
	}
	rollback := func(id string, want int) string {
		t.Helper()
		req, _ := http.NewRequest("POST", api.URL+"/api/projects/shop/rollback", strings.NewReader(`{"id":"`+id+`"}`))
		req.Header.Set("Authorization", "Bearer fixture")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != want {
			t.Fatalf("rollback to %s: %d %s", id, resp.StatusCode, b)
		}
		return string(b)
	}
	serving := func() string {
		t.Helper()
		aliases, err := st.Aliases(ctx)
		if err != nil || len(aliases) != 1 {
			t.Fatalf("aliases %v, %v", aliases, err)
		}
		return aliases[0].DeploymentID
	}

	rollback("a00001", 200)
	if got := serving(); got != "a00001" {
		t.Fatalf("production serves %s", got)
	}
	// a00001 serves and a00003 is where undo goes, so a00002 stops, after
	// the answer was sent.
	want := []string{"pier-app-a00001", "pier-app-a00003"}
	for deadline := time.Now().Add(5 * time.Second); !slices.Equal(engine.Running(), want); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("running containers = %v, want %v", engine.Running(), want)
		}
	}

	body := rollback("a00000", 409)
	if !strings.Contains(body, "couldn't be started, so production stays where it is") || !strings.Contains(body, "exit code 23") {
		t.Fatalf("a deployment that won't start was refused without saying why: %s", body)
	}
	if got := serving(); got != "a00001" {
		t.Fatalf("production moved to %s, which doesn't run", got)
	}
}
