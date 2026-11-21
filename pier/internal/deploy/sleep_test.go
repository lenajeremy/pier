package deploy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"pier/internal/apphost/dockertest"
	"pier/internal/router"
	"pier/internal/store"
)

// asleep gives a shop a waker and an idle time, as pierd does, with pierd
// started long enough ago that nothing is new because of it.
func (s *shop) asleep(after time.Duration) (visit func(id, host string) (int, string), visited map[string]time.Time) {
	s.t.Helper()
	waker := httptest.NewServer(s.d.WakeHandler())
	s.t.Cleanup(waker.Close)
	s.d.Waker = strings.TrimPrefix(waker.URL, "http://")
	s.d.SleepAfter = after
	s.d.boot = time.Now().Add(-24 * time.Hour)
	var mu sync.Mutex
	visited = map[string]time.Time{}
	s.d.Visited = func(id string) time.Time {
		mu.Lock()
		defer mu.Unlock()
		return visited[id]
	}
	// What Caddy sends the waker for an address of a sleeping deployment.
	visit = func(id, host string) (int, string) {
		s.t.Helper()
		req, _ := http.NewRequest("GET", waker.URL+"/cart", nil)
		req.Host = host
		req.Header.Set(router.WakeHeader, id)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			s.t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}
	return visit, visited
}

func (s *shop) sweep() {
	s.t.Helper()
	if err := s.d.StopSuperseded(context.Background(), ""); err != nil {
		s.t.Fatal(err)
	}
}

// A preview nobody visits sleeps, and its address wakes it: the visit that
// does waits for the start and is answered by the application.
func TestPreviewsSleepUntilTheNextVisit(t *testing.T) {
	ctx := context.Background()
	s := newShop(t)
	visit, visited := s.asleep(30 * time.Minute)
	up := dockertest.Container{Running: true}
	s.built("a00001", "main", store.TargetProduction, 1, up)
	s.point(store.KindProduction, "shop.pier.localhost", "a00001")
	s.built("b00001", "feat", store.TargetPreview, 2, up)
	s.point(store.KindBranch, "feat--shop.pier.localhost", "b00001")
	s.built("c00001", "fix", store.TargetPreview, 3, up) // pier deploy: its own address only
	s.built("d00001", "demo", store.TargetPreview, 4, up)
	if err := s.d.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	visited["c00001"] = time.Now().Add(-time.Minute)
	// Promoting a preview makes it production's, which never sleeps.
	if err := s.d.SetProduction(ctx, "shop", "d00001"); err != nil {
		t.Fatal(err)
	}

	s.sweep()
	s.running("a00001", "c00001", "d00001")
	r := s.route("feat--shop.pier.localhost")
	if r.Wake != s.d.Waker || r.Upstream != "" || r.Unavailable {
		t.Fatalf("a sleeping preview's address = %+v, want the waker", r)
	}
	if cfg := s.caddy.Load().(string); !strings.Contains(cfg, router.WakeHeader) || strings.Contains(cfg, s.addr["b00001"]) {
		t.Fatalf("the router doesn't send the sleeping preview's visits to the waker:\n%s", cfg)
	}

	code, body := visit("b00001", "feat--shop.pier.localhost")
	if code != 200 || body != "hello from pier-app-b00001" {
		t.Fatalf("waking visit = %d %q", code, body)
	}
	s.running("a00001", "b00001", "c00001", "d00001")
	if r := s.route("feat--shop.pier.localhost"); r.Upstream != s.addr["b00001"] || r.Wake != "" {
		t.Fatalf("the woken preview's address = %+v, want its container", r)
	}

	// Waking counts as a visit, so the next sweep leaves it up, until it has
	// gone a whole idle time without one.
	s.sweep()
	s.running("a00001", "b00001", "c00001", "d00001")
	s.d.wakeMu.Lock()
	s.d.woken["b00001"] = time.Now().Add(-31 * time.Minute)
	s.d.wakeMu.Unlock()
	visited["c00001"] = time.Now().Add(-31 * time.Minute)
	s.sweep()
	s.running("a00001", "d00001")
}

// Without an idle time, previews stay awake as they used to, but a
// superseded deployment someone opened still goes back to sleep.
func TestSleepAfterZeroKeepsPreviewsAwake(t *testing.T) {
	s := newShop(t)
	visit, _ := s.asleep(0)
	up := dockertest.Container{Running: true}
	for i, id := range []string{"a00001", "a00002", "a00003"} {
		s.built(id, "main", store.TargetProduction, i, up)
	}
	s.point(store.KindProduction, "shop.pier.localhost", "a00003")
	s.built("b00001", "feat", store.TargetPreview, 4, up)
	if err := s.d.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.sweep()
	s.running("a00002", "a00003", "b00001")

	if code, body := visit("a00001", "shop-a00001.pier.localhost"); code != 200 || !strings.Contains(body, "a00001") {
		t.Fatalf("visit to a superseded deployment = %d %q", code, body)
	}
	s.sweep()
	s.running("a00001", "a00002", "a00003", "b00001")
	s.d.wakeMu.Lock()
	s.d.woken["a00001"] = time.Now().Add(-wokenFor)
	s.d.wakeMu.Unlock()
	s.sweep()
	s.running("a00002", "a00003", "b00001")
}

// Restarting a deployment by hand keeps it up, like a visit, instead of
// the next sweep stopping it again.
func TestRestartKeepsASleepingDeploymentUp(t *testing.T) {
	ctx := context.Background()
	s := newShop(t)
	s.asleep(30 * time.Minute)
	for i, id := range []string{"a00001", "a00002", "a00003"} {
		s.built(id, "main", store.TargetProduction, i, dockertest.Container{Running: true})
	}
	s.point(store.KindProduction, "shop.pier.localhost", "a00003")
	s.sweep()
	s.running("a00002", "a00003")
	if err := s.d.Restart(ctx, "a00001", "pier-app-a00001"); err != nil {
		t.Fatal(err)
	}
	s.sweep()
	s.running("a00001", "a00002", "a00003")
}

// An application that won't start answers its visitors at once for a
// minute, instead of every visit holding a request while it fails again.
func TestWakingAnApplicationThatCrashes(t *testing.T) {
	s := newShop(t)
	visit, _ := s.asleep(30 * time.Minute)
	s.built("b00001", "feat", store.TargetPreview, 1, dockertest.Container{Crashes: true})
	s.point(store.KindBranch, "feat--shop.pier.localhost", "b00001")
	if code, body := visit("b00001", "feat--shop.pier.localhost"); code != 503 || body != "Application unavailable.\n" {
		t.Fatalf("visit to an application that crashes = %d %q", code, body)
	}
	starts := 0
	for _, c := range s.engine.Calls() {
		if c == "start pier-app-b00001" {
			starts++
		}
	}
	if code, _ := visit("b00001", "feat--shop.pier.localhost"); code != 503 {
		t.Fatalf("second visit = %d", code)
	}
	again := 0
	for _, c := range s.engine.Calls() {
		if c == "start pier-app-b00001" {
			again++
		}
	}
	if starts != 1 || again != 1 {
		t.Fatalf("starts = %d, then %d; want one start in the minute", starts, again)
	}

	// Only ready server deployments wake: not a static one, not a stranger.
	if code, _ := visit("nope", "x.pier.localhost"); code != 503 {
		t.Fatalf("visit naming no deployment = %d", code)
	}
}
