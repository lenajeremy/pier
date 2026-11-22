package deploy

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"pier/internal/alerts"
	"pier/internal/apphost/dockertest"
	"pier/internal/store"
)

// checked sets the shop's health check, puts pierd's clock where the test
// says, and returns a way to read production's health.
func (s *shop) checked(rt store.Runtime, now *time.Time) func(id string) store.Health {
	s.t.Helper()
	ctx := context.Background()
	p, err := s.d.Store.Project(ctx, "shop")
	if err != nil {
		s.t.Fatal(err)
	}
	if err := s.d.Store.SetProjectSettings(ctx, "shop", "", p.Build, rt); err != nil {
		s.t.Fatal(err)
	}
	s.d.clock = func() time.Time { return *now }
	return func(id string) store.Health {
		s.t.Helper()
		h, ok, err := s.d.Store.HealthOf(ctx, id)
		if err != nil || !ok {
			s.t.Fatalf("no health for %s: %v", id, err)
		}
		return h
	}
}

func restarts(calls []string) int {
	n := 0
	for _, c := range calls {
		if strings.HasPrefix(c, "restart ") {
			n++
		}
	}
	return n
}

// Three failed checks in a row restart production's container and send one
// alert; restarts that don't help wait longer each time; and the first check
// that passes marks it healthy and sends the recovery.
func TestHealthFailureRestartsAndRecovers(t *testing.T) {
	ctx := context.Background()
	s := newShop(t)
	h := &heard{}
	s.d.Alerts = h
	now := time.Now().Truncate(time.Millisecond)
	s.built("a00001", "main", store.TargetProduction, 1, dockertest.Container{Running: true})
	s.point(store.KindProduction, "shop.pier.localhost", "a00001")
	health := s.checked(store.Runtime{HealthPath: "/health"}, &now)

	s.d.CheckHealth(ctx)
	if got := health("a00001"); got.State != store.Healthy || got.Asked != "GET /health" || got.Failures != 0 {
		t.Fatalf("a healthy server: %+v", got)
	}

	s.engine.Answer("pier-app-a00001", "/health", 500)
	for range 2 {
		now = now.Add(HealthEvery)
		s.d.CheckHealth(ctx)
	}
	if got := health("a00001"); got.State != store.Failing || got.Failures != 2 || got.LastFailure != "GET /health answered 500 Internal Server Error" {
		t.Fatalf("after two failures: %+v", got)
	}
	if restarts(s.engine.Calls()) != 0 || len(h.raised) != 0 {
		t.Fatalf("two failures restarted it or alerted: %v, %d alerts", s.engine.Calls(), len(h.raised))
	}

	now = now.Add(HealthEvery)
	s.d.CheckHealth(ctx)
	got := health("a00001")
	if got.State != store.Unhealthy || got.Restarts != 1 || !got.NextRestart.Equal(now.Add(firstBackoff)) || !got.Since.Equal(now.Add(-2*HealthEvery)) {
		t.Fatalf("after the third failure: %+v", got)
	}
	if restarts(s.engine.Calls()) != 1 {
		t.Fatalf("calls = %v, want one restart", s.engine.Calls())
	}
	if len(h.raised) != 1 {
		t.Fatalf("raised %d alerts, want 1", len(h.raised))
	}
	e := h.raised[0]
	if e.Kind != alerts.HealthFailing || e.Key != "health:shop" || e.Deployment != "a00001" || e.Link != "/projects/shop/deployments/a00001" ||
		!strings.Contains(e.Summary, "shop.pier.localhost failed 3 health checks in a row, 30 seconds apart, because GET /health answered 500 Internal Server Error.") ||
		!strings.Contains(e.Summary, "Pier restarted its container") {
		t.Fatalf("alert = %+v", e)
	}

	// It still fails: no restart until the back-off has passed, then one
	// more, and the wait after it doubles. Still only the one alert.
	for range 3 {
		now = now.Add(15 * time.Second)
		s.d.CheckHealth(ctx)
	}
	if restarts(s.engine.Calls()) != 1 {
		t.Fatalf("restarted again within the back-off: %v", s.engine.Calls())
	}
	now = now.Add(firstBackoff)
	s.d.CheckHealth(ctx)
	if got := health("a00001"); restarts(s.engine.Calls()) != 2 || got.Restarts != 2 || !got.NextRestart.Equal(now.Add(2*firstBackoff)) {
		t.Fatalf("after the back-off: %v, %+v", s.engine.Calls(), got)
	}
	if len(h.raised) != 1 {
		t.Fatalf("a failure that goes on sent %d alerts", len(h.raised))
	}

	s.engine.Answer("pier-app-a00001", "/health", 0)
	now = now.Add(HealthEvery)
	s.d.CheckHealth(ctx)
	if got := health("a00001"); got.State != store.Healthy || got.Restarts != 0 || !got.NextRestart.IsZero() || got.LastFailure == "" {
		t.Fatalf("after it passes again: %+v", got)
	}
	last := h.resolved[len(h.resolved)-1]
	if last.Kind != alerts.HealthRecovered || last.Key != "health:shop" || last.Title != "shop: production is healthy again" {
		t.Fatalf("recovery = %+v", last)
	}
	events, _ := s.d.Store.Events(ctx, "shop", 0)
	var titles []string
	for _, ev := range events {
		titles = append(titles, ev.Title)
	}
	if !slices.Contains(titles, "Restarted an unhealthy container") || !slices.Contains(titles, "Passed its health check again") {
		t.Fatalf("activity = %v", titles)
	}
}

// Health checks never wake or flag a container that's asleep: a preview
// nobody visited, a superseded deployment, or one on its way to sleep.
// Only production is asked, and nothing is started.
func TestHealthChecksLeaveSleepersAlone(t *testing.T) {
	ctx := context.Background()
	s := newShop(t)
	h := &heard{}
	s.d.Alerts = h
	now := time.Now().Truncate(time.Millisecond)
	var mu sync.Mutex
	var probed []string
	s.engine.Probed = func(name string) {
		mu.Lock()
		defer mu.Unlock()
		probed = append(probed, name)
	}
	s.built("a00001", "main", store.TargetProduction, 1, dockertest.Container{})
	s.built("a00002", "main", store.TargetProduction, 2, dockertest.Container{Running: true})
	s.point(store.KindProduction, "shop.pier.localhost", "a00002")
	s.built("b00001", "feat", store.TargetPreview, 3, dockertest.Container{})
	s.point(store.KindBranch, "feat--shop.pier.localhost", "b00001")
	s.built("c00001", "demo", store.TargetPreview, 4, dockertest.Container{Running: true})
	health := s.checked(store.Runtime{HealthPath: "/health"}, &now)

	for range 4 {
		now = now.Add(HealthEvery)
		s.d.CheckHealth(ctx)
	}
	mu.Lock()
	asked := slices.Compact(slices.Sorted(slices.Values(probed)))
	mu.Unlock()
	if !slices.Equal(asked, []string{"pier-app-a00002"}) {
		t.Fatalf("health checks asked %v, want production alone", asked)
	}
	if calls := s.engine.Calls(); len(calls) != 0 {
		t.Fatalf("health checks changed containers: %v", calls)
	}
	for _, id := range []string{"a00001", "b00001", "c00001"} {
		if _, ok, _ := s.d.Store.HealthOf(ctx, id); ok {
			t.Fatalf("%s has a health record", id)
		}
	}
	if got := health("a00002"); got.State != store.Healthy {
		t.Fatalf("production: %+v", got)
	}

	// A container Pier is putting to sleep isn't asked either.
	mu.Lock()
	probed = nil
	mu.Unlock()
	s.d.endpointMu.Lock()
	s.d.stopping = map[string]bool{"pier-app-a00002": true}
	s.d.endpointMu.Unlock()
	s.engine.Exit("pier-app-a00002")
	for range 4 {
		now = now.Add(HealthEvery)
		s.d.CheckHealth(ctx)
	}
	if got := health("a00002"); got.State != store.Healthy || got.Failures != 0 || len(h.raised) != 0 || len(s.engine.Calls()) != 0 {
		t.Fatalf("a container going to sleep was flagged: %+v, %v", got, s.engine.Calls())
	}
}

// With the request turned off, only the process is watched: one that has
// stopped is restarted, and nothing is asked over HTTP.
func TestHealthCheckOfTheProcessOnly(t *testing.T) {
	ctx := context.Background()
	s := newShop(t)
	h := &heard{}
	s.d.Alerts = h
	now := time.Now().Truncate(time.Millisecond)
	s.built("a00001", "main", store.TargetProduction, 1, dockertest.Container{Running: true})
	s.point(store.KindProduction, "shop.pier.localhost", "a00001")
	health := s.checked(store.Runtime{HealthPath: "/health", HealthOff: true}, &now)
	s.engine.Answer("pier-app-a00001", "/health", 500) // not asked, so it doesn't matter

	s.d.CheckHealth(ctx)
	if got := health("a00001"); got.State != store.Healthy || got.Asked != "the process" {
		t.Fatalf("process only: %+v", got)
	}
	s.engine.Exit("pier-app-a00001")
	for range 3 {
		now = now.Add(HealthEvery)
		s.d.CheckHealth(ctx)
	}
	if restarts(s.engine.Calls()) != 1 || len(h.raised) != 1 || !strings.Contains(h.raised[0].Summary, "because its process isn't running") {
		t.Fatalf("a stopped process: %v, %+v", s.engine.Calls(), h.raised)
	}
	now = now.Add(HealthEvery)
	s.d.CheckHealth(ctx)
	if got := health("a00001"); got.State != store.Healthy {
		t.Fatalf("after the restart: %+v", got)
	}
}
