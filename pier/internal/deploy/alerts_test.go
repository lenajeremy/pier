package deploy

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"pier/internal/alerts"
)

// heard is an Alerter that keeps what it was told.
type heard struct {
	mu       sync.Mutex
	raised   []alerts.Event
	resolved []alerts.Event
}

func (h *heard) Raise(_ context.Context, e alerts.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.raised = append(h.raised, e)
}

func (h *heard) Resolve(_ context.Context, e alerts.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.resolved = append(h.resolved, e)
}

// A failed build raises its incident with the end of its log, and the next
// build of the same address that works resolves it.
func TestBuildsTellAlerts(t *testing.T) {
	caddy, _ := fakeCaddy(t)
	d := newDeployer(t, caddy)
	d.Logs = filepath.Join(t.TempDir(), "logs")
	h := &heard{}
	d.Alerts = h
	ctx := context.Background()

	good := gitRepo(t, d, "blog", "main", map[string]string{"index.html": "<h1>blog</h1>"})
	if err := d.Deploy(ctx, "blog", "main", good, io.Discard); err != nil {
		t.Fatal(err)
	}
	bad := gitRepo(t, d, "blog", "main", map[string]string{"README.md": "nothing to serve"})
	if err := d.Deploy(ctx, "blog", "main", bad, io.Discard); err == nil {
		t.Fatal("a push with nothing to serve should fail")
	}
	if len(h.raised) != 1 {
		t.Fatalf("raised %d alerts, want 1", len(h.raised))
	}
	e := h.raised[0]
	if e.Kind != alerts.DeployFailed || e.Key != "deploy:blog:production" || e.Preview || e.Title != "blog: production deploy failed" ||
		e.Branch != "main" || e.Commit != bad || !strings.HasPrefix(e.Link, "/projects/blog/deployments/") {
		t.Fatalf("raised %+v", e)
	}
	if !strings.Contains(e.Summary, "from main at "+bad[:7]+" failed after") || !strings.Contains(e.Summary, "blog.pier.localhost still serves ") {
		t.Fatalf("summary = %q", e.Summary)
	}
	if !strings.Contains(strings.Join(e.Lines, "\n"), "pier    build failed after") {
		t.Fatalf("lines = %q", e.Lines)
	}

	fixed := gitRepo(t, d, "blog", "main", map[string]string{"index.html": "<h1>fixed</h1>"})
	if err := d.Deploy(ctx, "blog", "main", fixed, io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(h.resolved) != 2 || h.resolved[1].Key != "deploy:blog:production" || h.resolved[1].Kind != alerts.DeployRecovered {
		t.Fatalf("resolved %+v", h.resolved)
	}

	// A branch's previews are an incident of their own, and can be silenced.
	if err := d.Deploy(ctx, "blog", "feat", gitRepo(t, d, "blog", "feat", map[string]string{"README.md": "no"}), io.Discard); err == nil {
		t.Fatal("a preview with nothing to serve should fail")
	}
	if p := h.raised[len(h.raised)-1]; p.Key != "deploy:blog:preview:feat" || !p.Preview || p.Title != "blog: preview of feat failed" {
		t.Fatalf("preview raised %+v", p)
	}
}
