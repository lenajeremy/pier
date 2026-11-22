package jobs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"pier/internal/alerts"
	"pier/internal/apphost"
	"pier/internal/deploy"
	"pier/internal/secrets"
	"pier/internal/store"
)

// commands stands in for Docker: it keeps what it was asked to run, prints
// lines, exits with a code, and waits for release when there is one.
type commands struct {
	mu      sync.Mutex
	specs   []apphost.JobSpec
	exit    int
	print   []apphost.LogLine
	release chan struct{}
}

func (c *commands) RunJob(ctx context.Context, spec apphost.JobSpec, emit func(apphost.LogLine) error) (int, error) {
	c.mu.Lock()
	c.specs = append(c.specs, spec)
	exit, print, release := c.exit, c.print, c.release
	c.mu.Unlock()
	for _, l := range print {
		emit(l)
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return 137, ctx.Err()
		}
	}
	return exit, nil
}

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

// fixture is a scheduler for the project shop, whose production is a
// server (runtime true) or a static site.
func fixture(t *testing.T, runtime bool) (*Scheduler, *commands, *heard) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "pier.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, err := secrets.Load(filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	d := &deploy.Deployer{Store: st, Secrets: box, Domain: "pier.localhost", URL: func(h string) string { return "http://" + h }, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := st.CreateProject(ctx, store.Project{Name: "shop", Runtime: store.Runtime{MemoryMB: 512}}); err != nil {
		t.Fatal(err)
	}
	dep := store.Deployment{ID: "a00001", Project: "shop", Branch: "main", Target: store.TargetProduction, Status: store.Building}
	if err := st.CreateDeployment(ctx, dep); err != nil {
		t.Fatal(err)
	}
	if runtime {
		st.TrackApp(ctx, dep.ID, "pier-app-shop")
	}
	dep.Status = store.Ready
	st.FinishDeployment(ctx, dep)
	st.SetAlias(ctx, store.Alias{Host: "shop.pier.localhost", Project: "shop", Kind: store.KindProduction, Branch: "main", DeploymentID: dep.ID})
	cmds, h := &commands{}, &heard{}
	s := &Scheduler{Store: st, Deployer: d, Alerts: h, Log: d.Log, Logs: filepath.Join(dir, "jobs"), Commands: cmds}
	return s, cmds, h
}

func (s *Scheduler) job(t *testing.T, j store.Job) store.Job {
	t.Helper()
	j.Project, j.Enabled = "shop", true
	if j.Timezone == "" {
		j.Timezone = "UTC"
	}
	id, err := s.Store.CreateJob(context.Background(), j)
	if err != nil {
		t.Fatal(err)
	}
	j.ID = id
	return j
}

func (s *Scheduler) history(t *testing.T, job int64) []store.JobRun {
	t.Helper()
	runs, _, err := s.Store.Runs(context.Background(), job, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(runs) // oldest first
	return runs
}

func minute(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse("2006-01-02 15:04", s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// A command job runs on its schedule, in production's image with the app's
// variables and memory, keeps its output, and alerts when it fails and
// when it works again.
func TestCommandJob(t *testing.T) {
	ctx := context.Background()
	s, cmds, h := fixture(t, true)
	j := s.job(t, store.Job{Name: "cleanup", Kind: store.JobCommand, Schedule: "*/5 * * * *", Command: "node cleanup.js", TimeoutS: 60})
	cmds.print = []apphost.LogLine{{Stream: "stdout", Text: "removed 12 sessions"}}

	s.Tick(ctx, minute(t, "2026-11-22 10:07"))
	s.Wait()
	if runs := s.history(t, j.ID); len(runs) != 0 {
		t.Fatalf("a minute the schedule doesn't name ran %d", len(runs))
	}
	s.Tick(ctx, minute(t, "2026-11-22 10:05"))
	s.Wait()
	runs := s.history(t, j.ID)
	if len(runs) != 1 || runs[0].Status != store.RunSucceeded || runs[0].ExitCode == nil || *runs[0].ExitCode != 0 ||
		runs[0].StartedBy != store.BySchedule || runs[0].Deployment != "a00001" || runs[0].FinishedAt.IsZero() {
		t.Fatalf("runs = %+v", runs)
	}
	spec := cmds.specs[0]
	if spec.App != "pier-app-shop" || spec.Command != "node cleanup.js" || spec.MemoryMB != 512 || !slices.ContainsFunc(spec.Env, func(kv string) bool { return strings.HasPrefix(kv, "CRON_SECRET=") }) {
		t.Fatalf("spec = %+v", spec)
	}
	lines, _ := ReadLog(s.Logs, "shop", runs[0].ID, 0)
	var text []string
	for _, l := range lines {
		text = append(text, l.Stream+" "+l.Text)
	}
	if !slices.Contains(text, "pier $ node cleanup.js") || !slices.Contains(text, "stdout removed 12 sessions") {
		t.Fatalf("log = %q", text)
	}

	cmds.exit = 3
	cmds.print = []apphost.LogLine{{Stream: "stderr", Text: "error: could not reach the database"}}
	s.Tick(ctx, minute(t, "2026-11-22 10:10"))
	s.Wait()
	runs = s.history(t, j.ID)
	if last := runs[len(runs)-1]; last.Status != store.RunFailed || *last.ExitCode != 3 || last.Detail != "It exited with code 3." {
		t.Fatalf("failed run = %+v", last)
	}
	if len(h.raised) != 1 {
		t.Fatalf("raised %d alerts", len(h.raised))
	}
	e := h.raised[0]
	if e.Kind != alerts.JobFailed || e.Key != "job:shop:1" || e.Title != "shop: job cleanup failed" || e.Job != "cleanup" ||
		!strings.HasPrefix(e.Summary, "The run at 10:10 UTC failed after") || !strings.Contains(e.Summary, "It exited with code 3.") ||
		!slices.Contains(e.Lines, "stderr  error: could not reach the database") || !strings.HasPrefix(e.Link, "/projects/shop/jobs/1/runs/") {
		t.Fatalf("alert = %+v", e)
	}

	cmds.exit = 0
	if _, err := s.RunNow(ctx, j); err != nil {
		t.Fatal(err)
	}
	s.Wait()
	last := h.resolved[len(h.resolved)-1]
	if last.Kind != alerts.JobRecovered || last.Key != "job:shop:1" || last.Summary != "The run started by hand succeeded." {
		t.Fatalf("recovery = %+v", last)
	}
}

// A run that's due while the last one still goes is skipped and recorded,
// and Run now says it's running.
func TestNoOverlappingRuns(t *testing.T) {
	ctx := context.Background()
	s, cmds, _ := fixture(t, true)
	j := s.job(t, store.Job{Name: "report", Kind: store.JobCommand, Schedule: "* * * * *", Command: "sleep 600", TimeoutS: 900})
	cmds.release = make(chan struct{})
	s.Tick(ctx, minute(t, "2026-11-22 10:05"))
	for range 100 {
		if s.Running(j.ID) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.Tick(ctx, minute(t, "2026-11-22 10:06"))
	if _, err := s.RunNow(ctx, j); !errors.Is(err, ErrRunning) {
		t.Fatalf("Run now while it runs: %v", err)
	}
	close(cmds.release)
	s.Wait()
	runs := s.history(t, j.ID)
	if len(runs) != 2 || runs[0].Status != store.RunSucceeded || runs[1].Status != store.RunSkipped ||
		runs[1].Detail != "The run before it was still going, so this one didn't start." {
		t.Fatalf("runs = %+v", runs)
	}
	if len(cmds.specs) != 1 {
		t.Fatalf("ran %d commands at once", len(cmds.specs))
	}
	if last, _ := s.Store.LastRuns(ctx, "shop"); last[j.ID].Status != store.RunSucceeded {
		t.Fatalf("the last run shown is %+v, not the one that ran", last[j.ID])
	}
}

// A run that goes past its timeout is stopped and says so.
func TestJobTimeout(t *testing.T) {
	ctx := context.Background()
	s, cmds, h := fixture(t, true)
	j := s.job(t, store.Job{Name: "slow", Kind: store.JobCommand, Schedule: "0 * * * *", Command: "sleep 600", TimeoutS: 1})
	cmds.release = make(chan struct{})
	defer close(cmds.release)
	s.RunNow(ctx, j)
	s.Wait()
	runs := s.history(t, j.ID)
	if runs[0].Status != store.RunFailed || runs[0].Detail != "It ran past its 1 second timeout and was stopped." || len(h.raised) != 1 {
		t.Fatalf("runs = %+v", runs)
	}
}

// A static site has no image, so a command can't run, and the run says so.
func TestStaticSitesCantRunCommands(t *testing.T) {
	ctx := context.Background()
	s, cmds, _ := fixture(t, false)
	j := s.job(t, store.Job{Name: "cleanup", Kind: store.JobCommand, Schedule: "0 * * * *", Command: "true", TimeoutS: 60})
	s.RunNow(ctx, j)
	s.Wait()
	if runs := s.history(t, j.ID); runs[0].Status != store.RunFailed || runs[0].Detail != "shop's production is a static site, so there's no image to run a command in." || len(cmds.specs) != 0 {
		t.Fatalf("runs = %+v", runs)
	}
}

// A path job requests production through the router with the project's
// cron secret as a bearer token, and counts any 2xx as success.
func TestPathJob(t *testing.T) {
	ctx := context.Background()
	s, _, h := fixture(t, true)
	secret, _, err := s.Deployer.CronSecret(ctx, "shop")
	if err != nil || len(secret) != 64 {
		t.Fatalf("cron secret = %q, %v", secret, err)
	}
	var mu sync.Mutex
	var seen []string
	status := http.StatusOK
	router := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.Host+r.URL.RequestURI()+" "+r.Header.Get("Authorization")+" "+r.Header.Get("X-Pier-Job"))
		code := status
		mu.Unlock()
		w.WriteHeader(code)
		io.WriteString(w, "cleaned 12\n")
	}))
	t.Cleanup(router.Close)
	s.Sites = strings.TrimPrefix(router.URL, "http://")
	j := s.job(t, store.Job{Name: "nightly", Kind: store.JobPath, Schedule: "0 3 * * *", Method: http.MethodPost, Path: "/api/cron?full=1", TimeoutS: 60})

	s.Tick(ctx, minute(t, "2026-11-22 03:00"))
	s.Wait()
	runs := s.history(t, j.ID)
	if len(runs) != 1 || runs[0].Status != store.RunSucceeded || runs[0].HTTPStatus == nil || *runs[0].HTTPStatus != 200 {
		t.Fatalf("runs = %+v", runs)
	}
	if want := "POST shop.pier.localhost/api/cron?full=1 Bearer " + secret + " nightly"; len(seen) != 1 || seen[0] != want {
		t.Fatalf("the router got %q, want %q", seen, want)
	}
	lines, _ := ReadLog(s.Logs, "shop", runs[0].ID, 0)
	if len(lines) < 3 || lines[1].Text != "It answered 200 OK in "+strings.TrimPrefix(lines[1].Text, "It answered 200 OK in ") || lines[2].Stream != "body" || lines[2].Text != "cleaned 12" {
		t.Fatalf("log = %+v", lines)
	}

	// The project's own CRON_SECRET wins, and is what's sent.
	box := s.Deployer.Secrets
	sealed, _ := box.Seal("my-own-secret", deploy.SealedFor("shop", store.TargetProduction, "CRON_SECRET"))
	s.Store.SetVariable(ctx, store.Variable{Project: "shop", Scope: store.TargetProduction, Name: "CRON_SECRET", Value: sealed})
	mu.Lock()
	status = http.StatusInternalServerError
	mu.Unlock()
	s.RunNow(ctx, j)
	s.Wait()
	runs = s.history(t, j.ID)
	if last := runs[len(runs)-1]; last.Status != store.RunFailed || *last.HTTPStatus != 500 || last.Detail != "POST /api/cron?full=1 answered 500 Internal Server Error." {
		t.Fatalf("failed run = %+v", last)
	}
	if !strings.Contains(seen[1], "Bearer my-own-secret") {
		t.Fatalf("sent %q", seen[1])
	}
	if len(h.raised) != 1 || !slices.Contains(h.raised[0].Lines, "body    cleaned 12") {
		t.Fatalf("alerts = %+v", h.raised)
	}

	// With nothing answering, the run says so.
	router.Close()
	s.RunNow(ctx, j)
	s.Wait()
	runs = s.history(t, j.ID)
	if last := runs[len(runs)-1]; last.Status != store.RunFailed || !strings.HasPrefix(last.Detail, "The request got no answer: ") {
		t.Fatalf("run with no router = %+v", last)
	}
}

// Without production there's nothing to run against.
func TestJobWithoutProduction(t *testing.T) {
	ctx := context.Background()
	s, _, _ := fixture(t, true)
	s.Store.CreateProject(ctx, store.Project{Name: "empty"})
	id, _ := s.Store.CreateJob(ctx, store.Job{Project: "empty", Name: "x", Kind: store.JobPath, Schedule: "0 * * * *", Path: "/", Timezone: "UTC", TimeoutS: 60, Enabled: true})
	j, _ := s.Store.JobByID(ctx, "empty", id)
	s.RunNow(ctx, j)
	s.Wait()
	if runs := s.history(t, id); runs[0].Detail != "empty has nothing in production to run it against yet." {
		t.Fatalf("runs = %+v", runs)
	}
}
