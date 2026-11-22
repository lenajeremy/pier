package jobs

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"pier/internal/alerts"
	"pier/internal/apphost"
	"pier/internal/deploy"
	"pier/internal/store"
)

// Commands runs a job's command in a fresh container. apphost.Host is one;
// tests use their own.
type Commands interface {
	RunJob(ctx context.Context, spec apphost.JobSpec, emit func(apphost.LogLine) error) (int, error)
}

// Scheduler starts each project's jobs on their schedules, and when asked.
type Scheduler struct {
	Store    *store.Store
	Deployer *deploy.Deployer
	Alerts   deploy.Alerter // nil sends none
	Log      *slog.Logger
	Logs     string   // where each run's output is kept
	Commands Commands // nil runs no commands
	// Sites is the router's own listener, which a path is requested
	// through, as a visitor's request would be; HTTPS says it speaks TLS.
	Sites string
	HTTPS bool
	// KeepRuns is how long a run's record and output are kept.
	KeepRuns time.Duration

	mu      sync.Mutex
	running map[int64]context.CancelFunc // job -> its run under way
	runs    sync.WaitGroup
	zones   map[string]*time.Location
}

// KeepRuns is how long runs are kept unless a Scheduler says otherwise.
const KeepRuns = 30 * 24 * time.Hour

// DefaultTimeout is how long a run may take unless its job says otherwise.
const DefaultTimeout = 15 * time.Minute

// ErrRunning means the job's last run is still going.
var ErrRunning = errors.New("it's running now; a new run starts once this one ends")

// Run starts jobs at the start of each minute their schedules name, until
// ctx ends; then it stops the runs under way, for a while at most. Minutes
// that pass while pierd is stopped, or while the machine sleeps, are
// skipped, not made up.
func (s *Scheduler) Run(ctx context.Context) {
	s.recover(ctx)
	for {
		next := time.Now().Truncate(time.Minute).Add(time.Minute)
		t := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			t.Stop()
			s.stop()
			return
		case <-t.C:
		}
		s.Tick(ctx, next)
		if next.Minute() == 17 {
			s.prune(ctx)
		}
	}
}

// recover ends the runs a stop cut short, and removes their containers.
func (s *Scheduler) recover(ctx context.Context) {
	if n, err := s.Store.InterruptRuns(ctx); err != nil {
		s.Log.Warn("jobs: end interrupted runs", "err", err)
	} else if n > 0 {
		s.Log.Info("jobs: ended runs pierd stopped during", "runs", n)
	}
	if h, ok := s.Commands.(*apphost.Host); ok && h != nil {
		if err := h.RemoveJobs(ctx); err != nil {
			s.Log.Warn("jobs: remove the containers of interrupted runs", "err", err)
		}
	}
}

// stop cancels the runs under way and waits up to 20 seconds for them to
// record how they ended.
func (s *Scheduler) stop() {
	s.mu.Lock()
	for _, cancel := range s.running {
		cancel()
	}
	s.mu.Unlock()
	done := make(chan struct{})
	go func() { s.runs.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
	}
}

// Wait waits for the runs under way.
func (s *Scheduler) Wait() { s.runs.Wait() }

func (s *Scheduler) zone(name string) *time.Location {
	s.mu.Lock()
	defer s.mu.Unlock()
	if loc, ok := s.zones[name]; ok {
		return loc
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		loc = time.UTC
	}
	if s.zones == nil {
		s.zones = map[string]*time.Location{}
	}
	s.zones[name] = loc
	return loc
}

// Tick starts every enabled job whose schedule names the minute at t.
func (s *Scheduler) Tick(ctx context.Context, t time.Time) {
	jobs, err := s.Store.EnabledJobs(ctx)
	if err != nil {
		s.Log.Warn("jobs: list what's scheduled", "err", err)
		return
	}
	for _, j := range jobs {
		sched, err := Parse(j.Schedule)
		if err != nil || !sched.Matches(t.In(s.zone(j.Timezone))) {
			continue
		}
		if _, err := s.start(ctx, j, store.BySchedule, t); err != nil && !errors.Is(err, ErrRunning) {
			s.Log.Warn("jobs: start a run", "project", j.Project, "job", j.Name, "err", err)
		}
	}
}

// RunNow starts a run of a job at once, unless one is still going.
func (s *Scheduler) RunNow(ctx context.Context, j store.Job) (int64, error) {
	return s.start(ctx, j, store.ByHand, time.Time{})
}

// Running reports whether a job has a run under way.
func (s *Scheduler) Running(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running[id] != nil
}

// start begins a run in the background and returns its ID. A run that's
// due while the last one still goes is recorded as skipped instead.
func (s *Scheduler) start(ctx context.Context, j store.Job, by string, due time.Time) (int64, error) {
	now := time.Now()
	s.mu.Lock()
	if s.running[j.ID] != nil {
		s.mu.Unlock()
		if by == store.BySchedule {
			_, err := s.Store.StartRun(ctx, store.JobRun{
				Job: j.ID, Project: j.Project, StartedBy: by, Status: store.RunSkipped, ScheduledFor: due,
				StartedAt: now, FinishedAt: now, Detail: "The run before it was still going, so this one didn't start.",
			})
			if err != nil {
				return 0, err
			}
			s.Log.Info("jobs: skipped a run; the last one is still going", "project", j.Project, "job", j.Name)
		}
		return 0, ErrRunning
	}
	if s.running == nil {
		s.running = map[int64]context.CancelFunc{}
	}
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.running[j.ID] = cancel
	s.mu.Unlock()
	run := store.JobRun{Job: j.ID, Project: j.Project, StartedBy: by, Status: store.RunRunning, ScheduledFor: due, StartedAt: now}
	id, err := s.Store.StartRun(ctx, run)
	if err != nil {
		s.done(j.ID)
		return 0, err
	}
	run.ID = id
	s.runs.Add(1)
	go func() {
		defer s.runs.Done()
		defer s.done(j.ID)
		s.execute(runCtx, j, run)
	}()
	return id, nil
}

func (s *Scheduler) done(job int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cancel := s.running[job]; cancel != nil {
		cancel()
	}
	delete(s.running, job)
}

// execute runs one run to its end and records how it went.
func (s *Scheduler) execute(ctx context.Context, j store.Job, run store.JobRun) {
	log, err := openLog(s.Logs, j.Project, run.ID, run.StartedAt)
	if err != nil {
		s.Log.Warn("jobs: a run's output can't be kept", "err", err)
	}
	timeout := time.Duration(j.TimeoutS) * time.Second
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	limited, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var failure string
	dep, live, err := s.Deployer.Production(ctx, j.Project)
	switch {
	case err != nil:
		failure = "Pier couldn't tell what production serves: " + err.Error()
	case !live:
		failure = j.Project + " has nothing in production to run it against yet."
	case j.Kind == store.JobCommand:
		run.Deployment = dep.ID
		failure = s.command(limited, j, dep, &run, log)
	default:
		run.Deployment = dep.ID
		failure = s.path(limited, j, &run, log)
	}
	if failure != "" && limited.Err() != nil {
		switch {
		case ctx.Err() != nil:
			failure = "pierd stopped during this run."
		case errors.Is(limited.Err(), context.DeadlineExceeded):
			failure = "It ran past its " + alerts.Span(timeout) + " timeout and was stopped."
		}
	}
	run.FinishedAt = time.Now()
	run.Status, run.Detail = store.RunSucceeded, ""
	if failure != "" {
		run.Status, run.Detail = store.RunFailed, failure
		log.say("%s", failure)
	} else {
		log.say("Finished in %s.", took(run.FinishedAt.Sub(run.StartedAt)))
	}
	log.close()
	if err := s.Store.FinishRun(context.WithoutCancel(ctx), run); err != nil {
		s.Log.Warn("jobs: record how a run ended", "err", err)
	}
	if ctx.Err() == nil {
		s.report(context.WithoutCancel(ctx), j, run, log.last())
	}
}

// command runs the job's command in a fresh container from production's
// image, with production's variables and the app's limits. It returns why
// it failed, or "" when it worked.
func (s *Scheduler) command(ctx context.Context, j store.Job, dep store.Deployment, run *store.JobRun, log *runLog) string {
	if dep.Runtime == "" {
		return j.Project + "'s production is a static site, so there's no image to run a command in."
	}
	if s.Commands == nil {
		return "This server can't run containers."
	}
	env, err := s.Deployer.Variables(ctx, j.Project, store.TargetProduction)
	if err != nil {
		return "Pier couldn't open the project's variables: " + err.Error()
	}
	p, err := s.Store.Project(ctx, j.Project)
	if err != nil {
		return err.Error()
	}
	log.say("$ %s", j.Command)
	log.say("in a fresh container from %s, production's image", dep.ID)
	code, err := s.Commands.RunJob(ctx, apphost.JobSpec{
		App: dep.Runtime, Command: j.Command, Env: env, MemoryMB: p.Runtime.MemoryMB, CPUs: p.Runtime.CPUs,
		Run: strconv.FormatInt(run.ID, 10),
	}, func(l apphost.LogLine) error {
		log.line(l.Stream, l.Text)
		return nil
	})
	if code >= 0 {
		run.ExitCode = &code
	}
	switch {
	case err != nil && ctx.Err() != nil:
		return "stopped"
	case errors.Is(err, apphost.ErrNoImage):
		return "Production's image isn't on this server any more. Redeploy production, and the next run uses the new one."
	case err != nil:
		return "The container didn't run: " + err.Error()
	case code != 0:
		return fmt.Sprintf("It exited with code %d.", code)
	}
	return ""
}

// pathClient requests a job's path through the router's own listener, as a
// visitor would reach production, so a sleeping app wakes for it. The
// connection never leaves the machine, so the certificate isn't checked:
// it may not have been issued yet. Redirects aren't followed, so the
// secret is never sent anywhere else.
func (s *Scheduler) pathClient(host string) *http.Client {
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", s.Sites)
	}
	return &http.Client{
		Transport: &http.Transport{
			DialContext:       dial,
			TLSClientConfig:   &tls.Config{ServerName: host, InsecureSkipVerify: true},
			DisableKeepAlives: true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// path requests the job's path on production with the cron secret, and
// counts any 2xx answer as success.
func (s *Scheduler) path(ctx context.Context, j store.Job, run *store.JobRun, log *runLog) string {
	if s.Sites == "" {
		return "This server has no site listener to send the request through."
	}
	env, err := s.Deployer.Variables(ctx, j.Project, store.TargetProduction)
	if err != nil {
		return "Pier couldn't open the project's variables: " + err.Error()
	}
	secret := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, deploy.CronSecretName+"="); ok {
			secret = v
		}
	}
	host := s.Deployer.ProductionHost(ctx, j.Project)
	scheme := "http"
	if s.HTTPS {
		scheme = "https"
	}
	method := j.Method
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, scheme+"://"+host+j.Path, nil)
	if err != nil {
		return err.Error()
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("User-Agent", "Pier-Cron/1.0")
	req.Header.Set("X-Pier-Job", j.Name)
	log.say("%s %s%s", method, host, j.Path)
	start := time.Now()
	resp, err := s.pathClient(host).Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "stopped"
		}
		return "The request got no answer: " + cause(err)
	}
	defer resp.Body.Close()
	code := resp.StatusCode
	run.HTTPStatus = &code
	log.say("It answered %s in %s.", resp.Status, took(time.Since(start)))
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	for _, l := range strings.Split(strings.TrimRight(string(body), "\n"), "\n") {
		if strings.TrimSpace(l) != "" {
			log.line("body", l)
		}
	}
	if code < 200 || code > 299 {
		return fmt.Sprintf("%s %s answered %s.", method, j.Path, resp.Status)
	}
	return ""
}

func cause(err error) string {
	var oerr *net.OpError
	if errors.As(err, &oerr) && oerr.Err != nil {
		return oerr.Err.Error()
	}
	return err.Error()
}

func took(d time.Duration) string {
	if d < time.Second {
		return strconv.FormatInt(d.Milliseconds(), 10) + " ms"
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return alerts.Span(d)
}

// report tells Alerts about a run: a failure raises the job's incident,
// and a run that works resolves it.
func (s *Scheduler) report(ctx context.Context, j store.Job, run store.JobRun, lines []string) {
	if s.Alerts == nil {
		return
	}
	when := "The run started by hand"
	if run.StartedBy == store.BySchedule && !run.ScheduledFor.IsZero() {
		when = "The run at " + run.ScheduledFor.In(s.zone(j.Timezone)).Format("15:04") + " " + zoneWord(j.Timezone)
	}
	e := alerts.Event{
		Key: "job:" + j.Project + ":" + strconv.FormatInt(j.ID, 10), Project: j.Project, Job: j.Name, Deployment: run.Deployment,
		Link: fmt.Sprintf("/projects/%s/jobs/%d/runs/%d", j.Project, j.ID, run.ID),
	}
	if run.Status == store.RunSucceeded {
		e.Kind, e.Title = alerts.JobRecovered, j.Project+": job "+j.Name+" works again"
		e.Summary = when + " succeeded."
		s.Alerts.Resolve(ctx, e)
		return
	}
	e.Kind, e.Title = alerts.JobFailed, j.Project+": job "+j.Name+" failed"
	e.Summary = when + " failed after " + took(run.FinishedAt.Sub(run.StartedAt)) + ". " + run.Detail
	e.Lines = lines
	s.Alerts.Raise(ctx, e)
}

func zoneWord(zone string) string {
	if zone == "" || zone == "UTC" {
		return "UTC"
	}
	return zone + " time"
}

// prune forgets runs older than KeepRuns, with their output.
func (s *Scheduler) prune(ctx context.Context) {
	keep := s.KeepRuns
	if keep == 0 {
		keep = KeepRuns
	}
	gone, err := s.Store.PruneRuns(ctx, time.Now().Add(-keep))
	if err != nil {
		s.Log.Warn("jobs: forget old runs", "err", err)
		return
	}
	for _, r := range gone {
		os.Remove(logPath(s.Logs, r.Project, r.ID))
	}
}

// Forget removes the output of runs whose records are gone.
func (s *Scheduler) Forget(project string, runs []int64) {
	for _, id := range runs {
		os.Remove(logPath(s.Logs, project, id))
	}
}

// ForgetProject removes the output of every run of a deleted project.
func (s *Scheduler) ForgetProject(project string) {
	if s.Logs != "" && project != "" {
		os.RemoveAll(filepath.Join(s.Logs, project))
	}
}
