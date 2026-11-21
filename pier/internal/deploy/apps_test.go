package deploy

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"pier/internal/apphost"
	"pier/internal/apphost/dockertest"
	"pier/internal/router"
	"pier/internal/store"
)

// shop is a project of server deployments whose containers live on a
// stand-in Docker engine.
type shop struct {
	t      *testing.T
	d      *Deployer
	engine *dockertest.Engine
	caddy  *atomic.Value // the last config loaded into the router
	addr   map[string]string
}

func newShop(t *testing.T) *shop {
	t.Helper()
	caddy, last := fakeCaddy(t)
	s := &shop{t: t, d: newDeployer(t, caddy), engine: dockertest.Start(t), caddy: last, addr: map[string]string{}}
	s.d.Apps = apphost.New(t.TempDir())
	if err := s.d.Store.EnsureProject(context.Background(), "shop"); err != nil {
		t.Fatal(err)
	}
	return s
}

// built records a server deployment that built minutes after the first one,
// with a container as an older Pier made it: running, and restarted always.
func (s *shop) built(id, branch, target string, minute int, c dockertest.Container) {
	s.t.Helper()
	ctx := context.Background()
	dep := store.Deployment{ID: id, Project: "shop", Branch: branch, Target: target, Status: store.Building, CreatedAt: time.Now().Add(time.Duration(minute-60) * time.Minute)}
	if err := s.d.Store.CreateDeployment(ctx, dep); err != nil {
		s.t.Fatal(err)
	}
	if err := s.d.Store.TrackApp(ctx, id, "pier-app-"+id); err != nil {
		s.t.Fatal(err)
	}
	dep.Status = store.Ready
	if err := s.d.Store.FinishDeployment(ctx, dep); err != nil {
		s.t.Fatal(err)
	}
	c.Name, c.Labels, c.Policy = "pier-app-"+id, s.d.Apps.Labels(), "always"
	s.addr[id] = s.engine.Add(c)
}

func (s *shop) point(kind, host, id string) {
	s.t.Helper()
	if err := s.d.Store.SetAlias(context.Background(), store.Alias{Kind: kind, Host: host, Project: "shop", DeploymentID: id}); err != nil {
		s.t.Fatal(err)
	}
}

func (s *shop) running(want ...string) {
	s.t.Helper()
	for i := range want {
		want[i] = "pier-app-" + want[i]
	}
	if got := s.engine.Running(); !slices.Equal(got, want) {
		s.t.Fatalf("running containers = %v, want %v", got, want)
	}
}

func (s *shop) route(host string) router.Route {
	s.t.Helper()
	routes, err := s.d.Routes(context.Background())
	if err != nil {
		s.t.Fatal(err)
	}
	i := slices.IndexFunc(routes, func(r router.Route) bool { return r.Host == host })
	if i < 0 {
		s.t.Fatalf("no route for %s", host)
	}
	return routes[i]
}

// Every push used to leave its container running for good. Only what an
// address serves and what a rollback needs at once may stay.
func TestStopSupersededKeepsWhatServes(t *testing.T) {
	ctx := context.Background()
	s := newShop(t)
	up := dockertest.Container{Running: true}
	for i, id := range []string{"a00001", "a00002", "a00003", "a00004"} {
		s.built(id, "main", store.TargetProduction, i, up)
	}
	s.point(store.KindProduction, "shop.pier.localhost", "a00004")
	s.built("b00001", "feat", store.TargetPreview, 1, up)
	s.built("b00002", "feat", store.TargetPreview, 2, up)
	s.point(store.KindBranch, "feat--shop.pier.localhost", "b00002")
	if err := s.d.Sync(ctx); err != nil {
		t.Fatal(err)
	}

	// While Docker doesn't answer nothing is stopped, and it is tried again.
	s.engine.SetDown(true)
	if err := s.d.StopSuperseded(ctx, "shop"); err == nil {
		t.Fatal("stopping worked without Docker")
	}
	s.engine.SetDown(false)
	s.running("a00001", "a00002", "a00003", "a00004", "b00001", "b00002")
	if err := s.d.RetryStops(ctx); err != nil {
		t.Fatal(err)
	}

	// Production, where a rollback would go, and the branch's newest stay.
	s.running("a00003", "a00004", "b00002")
	for _, id := range []string{"a00001", "a00002", "b00001"} {
		if got := s.engine.Policy("pier-app-" + id); got != "unless-stopped" {
			t.Errorf("%s would start again at the next reboot: restart policy %q", id, got)
		}
	}
	if r := s.route("shop.pier.localhost"); r.Upstream != s.addr["a00004"] || r.Unavailable {
		t.Fatalf("production route = %+v", r)
	}
	// A stopped deployment keeps its own address, which says so.
	if r := s.route("shop-a00001.pier.localhost"); !r.Unavailable || r.Upstream != "" {
		t.Fatalf("stopped deployment's route = %+v", r)
	}
	if cfg := s.caddy.Load().(string); !strings.Contains(cfg, "Application unavailable.") || strings.Contains(cfg, s.addr["a00001"]) {
		t.Fatalf("the router still proxies to a stopped container:\n%s", cfg)
	}

	// Nothing more to do, and nothing left to retry.
	before := len(s.engine.Calls())
	if err := s.d.StopSuperseded(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.d.RetryStops(ctx); err != nil || len(s.engine.Calls()) != before {
		t.Fatalf("a second sweep changed containers: %v, %v", s.engine.Calls()[before:], err)
	}
}

// A rollback to a stopped deployment starts it and waits for it to answer
// before production moves, and leaves production alone if it won't start.
func TestSetProductionStartsAStoppedDeployment(t *testing.T) {
	ctx := context.Background()
	s := newShop(t)
	s.built("a00000", "main", store.TargetProduction, 0, dockertest.Container{Crashes: true})
	s.built("a00001", "main", store.TargetProduction, 1, dockertest.Container{})
	s.built("a00002", "main", store.TargetProduction, 2, dockertest.Container{Running: true})
	s.built("a00003", "main", store.TargetProduction, 3, dockertest.Container{Running: true})
	s.point(store.KindProduction, "shop.pier.localhost", "a00003")
	if err := s.d.Sync(ctx); err != nil {
		t.Fatal(err)
	}

	var servedWhileStarting atomic.Value
	s.engine.Probed = func(name string) {
		if name == "pier-app-a00001" {
			servedWhileStarting.Store(s.d.serving(ctx, "shop.pier.localhost"))
		}
	}
	if err := s.d.SetProduction(ctx, "shop", "a00001"); err != nil {
		t.Fatal(err)
	}
	if got := servedWhileStarting.Load(); got != "a00003" {
		t.Fatalf("production served %v while the stopped deployment was starting, want a00003", got)
	}
	s.running("a00001", "a00002", "a00003")
	if got := s.d.serving(ctx, "shop.pier.localhost"); got != "a00001" {
		t.Fatalf("production serves %s", got)
	}
	if cfg := s.caddy.Load().(string); !strings.Contains(cfg, s.addr["a00001"]) {
		t.Fatalf("the router doesn't proxy to the started container:\n%s", cfg)
	}

	err := s.d.SetProduction(ctx, "shop", "a00000")
	if !errors.Is(err, ErrNotRunning) || !strings.Contains(err.Error(), "exit code 23") {
		t.Fatalf("moving production to a process that crashes: %v", err)
	}
	if got := s.d.serving(ctx, "shop.pier.localhost"); got != "a00001" {
		t.Fatalf("production moved to %s, which doesn't run", got)
	}

	// What the move left superseded stops afterwards: a00001 serves, a00000
	// is where a rollback would go, and a00003 is the branch's newest.
	if err := s.d.StopSuperseded(ctx, "shop"); err != nil {
		t.Fatal(err)
	}
	s.running("a00001", "a00003")
}

// One slow answer from Docker must not turn every application into a 503.
func TestRoutesKeepEndpointsWhenDockerDoesNotAnswer(t *testing.T) {
	ctx := context.Background()
	s := newShop(t)
	s.built("a00001", "main", store.TargetProduction, 1, dockertest.Container{Running: true})
	s.point(store.KindProduction, "shop.pier.localhost", "a00001")
	if err := s.d.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	s.caddy.Store("")

	s.engine.SetDown(true)
	if r := s.route("shop.pier.localhost"); r.Upstream != s.addr["a00001"] || r.Unavailable {
		t.Fatalf("route while Docker is busy = %+v", r)
	}
	if err := s.d.syncRoutes(ctx, false); err != nil || s.caddy.Load() != "" {
		t.Fatalf("a failed probe reloaded the router: %v\n%s", err, s.caddy.Load())
	}

	// A container that is really gone is still noticed once Docker answers.
	s.engine.SetDown(false)
	if err := s.d.Apps.Stop(ctx, "pier-app-a00001"); err != nil {
		t.Fatal(err)
	}
	if r := s.route("shop.pier.localhost"); !r.Unavailable {
		t.Fatalf("route to a stopped container = %+v", r)
	}
}
