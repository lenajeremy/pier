package main

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"pier/internal/apphost"
)

// The deployment detail's Server line says how production's health checks
// are going: why it fails, or that it passes and when it last didn't.
func TestServerLineShowsHealth(t *testing.T) {
	m := fixture(180, 30, true)
	live := detailOf(t, m, "8f3a2c")
	live.detail.Deployment.Runtime = "node"
	live.detail.Container = &apphost.ContainerStatus{State: "running", StartedAt: time.Now().Add(-2 * time.Minute)}
	server := func() string {
		t.Helper()
		for _, l := range strings.Split(ansi.Strip(live.View()), "\n") {
			if i := strings.Index(l, "Server"); i >= 0 {
				return strings.Join(strings.Fields(l[i:]), " ")
			}
		}
		t.Fatal("no Server line")
		return ""
	}

	live.detail.Health = &healthView{State: "unhealthy", Asked: "GET /health", LastFailure: "GET /health answered 500 Internal Server Error", RestartedAt: time.Now().Add(-90 * time.Second)}
	if got := server(); !strings.Contains(got, "● unhealthy last check: GET /health answered 500 Internal Server Error, restarted 1 min ago") {
		t.Errorf("failing: %q", got)
	}
	live.detail.Health.State = "restarting"
	if got := server(); !strings.HasPrefix(got, "Server ● restarting") {
		t.Errorf("restarting: %q", got)
	}
	live.detail.Health = &healthView{State: "healthy", Asked: "GET /health", LastFailureAt: time.Now().Add(-3 * time.Hour)}
	if got := server(); !strings.Contains(got, "● running health check passing, last failed 3 hours ago") {
		t.Errorf("passing: %q", got)
	}
}
