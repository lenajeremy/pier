package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func intp(n int) *int { return &n }

// withJobs is the fixture with landing's jobs loaded: a command job whose
// last run failed, a path job that answered 200, and one that's off.
func withJobs(w, h int, color bool) cockpit {
	m := fixture(w, h, color)
	at := func(d time.Duration) *time.Time { t := time.Now().Add(d); return &t }
	m.jobs = map[string]*projectJobs{"landing": {
		Jobs: []jobSummary{
			{ID: 1, Name: "nightly-cleanup", Kind: "command", Command: "npm run cleanup", Enabled: true, TimeoutS: 900,
				Description: "Every day at 03:00 UTC", NextRun: at(5 * time.Hour),
				LastRun: &jobRun{ID: 9, Status: "failed", ExitCode: intp(1), StartedAt: time.Now().Add(-time.Hour)}},
			{ID: 2, Name: "ping", Kind: "path", Method: "GET", Path: "/api/cron", Enabled: true,
				Description: "Every 5 minutes", NextRun: at(3 * time.Minute),
				LastRun: &jobRun{ID: 4, Status: "succeeded", HTTPStatus: intp(200), StartedAt: time.Now().Add(-2 * time.Minute)}},
			{ID: 3, Name: "monthly-report", Kind: "command", Command: "node report.js", Description: "On the 1st of every month at 08:00 UTC"},
		},
		Production: &struct {
			ID     string `json:"id"`
			Server bool   `json:"server"`
		}{ID: "8f3a2c", Server: true},
	}}
	return m
}

// manyRuns are the newest n of a job's total runs.
func manyRuns(n, total int) []jobRun {
	var out []jobRun
	for i := range n {
		r := jobRun{ID: int64(total - i), Status: "succeeded", StartedBy: "schedule", ExitCode: intp(0),
			StartedAt: time.Now().Add(-time.Duration(i+1) * time.Hour)}
		r.FinishedAt = r.StartedAt.Add(1200 * time.Millisecond)
		if i == 1 {
			r.Status, r.ExitCode, r.Detail, r.StartedBy, r.RunBy = "failed", intp(3), "It exited with code 3.", "manual", "Ada"
		}
		out = append(out, r)
	}
	return out
}

// onJob is the job screen of nightly-cleanup with runs loaded.
func onJob(t *testing.T, m cockpit, runs []jobRun, total int) cockpit {
	t.Helper()
	m = press(t, m, "J", "enter")
	if m.view != jobView || m.jobID != 1 {
		t.Fatalf("enter on the first job should open it, at %v %d", m.view, m.jobID)
	}
	m.loadingRuns = false
	m.keepRuns(runs, total, true)
	return m
}

func TestJobsScreensFitEverySize(t *testing.T) {
	for _, size := range sizes {
		w, h := size[0], size[1]
		for _, color := range []bool{true, false} {
			m := withJobs(w, h, color)
			checkScreen(t, "home with jobs", m.View(), w, h, "")
			jobs := press(t, m, "J")
			checkScreen(t, "jobs", jobs.View(), w, h, "enter open")
			job := onJob(t, m, manyRuns(25, 60), 60)
			checkScreen(t, "job", job.View(), w, h, "enter open")
			job.focus = actionsPane
			checkScreen(t, "job, the actions focused", press(t, job, "up").View(), w, h, "enter open")

			none := fixture(w, h, color)
			none.jobs = map[string]*projectJobs{"landing": {}}
			checkScreen(t, "no jobs", press(t, none, "J").View(), w, h, "enter open")

			out := press(t, job, "enter")
			out.logs.rows = []logRow{runRow(runLine{MS: 10, Stream: "pier", Text: "$ npm run cleanup"}),
				runRow(runLine{MS: 220, Stream: "stderr", Text: "could not reach the database"}), runEnd(manyRuns(2, 2)[1], "")}
			checkScreen(t, "run output", out.View(), w, h, "esc back")
		}
	}
}

func TestJobsScreensSayWhatTheyShould(t *testing.T) {
	m := withJobs(96, 30, true)
	home := ansi.Strip(m.View())
	if !strings.Contains(home, "Jobs (3)") || !strings.Contains(home, "J │") {
		t.Fatalf("home should offer the jobs with their count and key:\n%s", home)
	}
	jobs := ansi.Strip(press(t, m, "J").View())
	for _, want := range []string{"landing's jobs", "Job", "Schedule", "Last run", "Next run", "nightly-cleanup", "Every day at 03:00 UTC",
		"● failed", "● succeeded", "● not run", "in 5 hours", "off", "Open the Jobs tab in the dashboard", "Back to landing"} {
		if !strings.Contains(jobs, want) {
			t.Errorf("the jobs screen lacks %q:\n%s", want, jobs)
		}
	}
	job := ansi.Strip(onJob(t, m, manyRuns(25, 60), 60).View())
	for _, want := range []string{"Job nightly-cleanup", "npm run cleanup", "Every day at 03:00 UTC", "Enabled", "Yes", "15 minutes",
		"Run now", "Back to the jobs", "25 of 60, more as you scroll", "When", "Started", "Result", "Code", "Took",
		"on its schedule", "Run now by Ada", "● failed", "exit 3", "1.2s"} {
		if !strings.Contains(job, want) {
			t.Errorf("the job screen lacks %q:\n%s", want, job)
		}
	}

	none := fixture(96, 30, true)
	none.jobs = map[string]*projectJobs{"landing": {Production: &struct {
		ID     string `json:"id"`
		Server bool   `json:"server"`
	}{ID: "8f3a2c"}}}
	if !strings.Contains(ansi.Strip(none.View()), "Jobs (none yet)") {
		t.Error("a project that could have jobs offers them even with none")
	}
	empty := ansi.Strip(press(t, none, "J").View())
	if !strings.Contains(empty, "landing's jobs") || !strings.Contains(empty, "│ No jobs yet. Add one in the Jobs tab of pier's dashboard.") {
		t.Fatalf("no jobs should say so in a plain line:\n%s", empty)
	}

	nothing := fixture(96, 30, true)
	nothing.jobs = map[string]*projectJobs{"landing": {}}
	nothing.details["landing"].Deployments = nil
	if strings.Contains(ansi.Strip(nothing.View()), "Jobs (") {
		t.Error("a project without jobs or production has nothing to offer about jobs")
	}
	off := fixture(96, 30, true)
	off.jobs = map[string]*projectJobs{"landing": {unavailable: "Scheduled jobs aren't available on this server."}}
	if got := press(t, off, "J"); got.view != homeView || !strings.Contains(got.notice, "aren't available") {
		t.Fatal("J on a server without jobs should say so")
	}

	mono := press(t, withJobs(96, 30, false), "J").View()
	if strings.Contains(mono, "38;2;") || !strings.Contains(mono, "\x1b[7m") {
		t.Fatal("without colour the jobs have no colour, and the selected one is reversed")
	}
}

func TestJobsKeys(t *testing.T) {
	m := press(t, withJobs(96, 30, true), "J")
	if m.view != jobsView || m.jobsFocused() != projectPane || m.jCursor != 0 {
		t.Fatal("J should open the jobs with the first one selected")
	}
	if got := press(t, m, "down", "down", "down"); got.jobsFocused() != actionsPane || got.actKeys[jobsView] != "o" {
		t.Fatal("moving down off the last job reaches the actions")
	}
	if got := press(t, m, "down", "down", "down", "down", "enter"); got.view != homeView {
		t.Fatal("Back to landing goes home")
	}
	if got := press(t, m, "esc"); got.view != homeView {
		t.Fatal("esc goes home from the jobs")
	}

	job := onJob(t, withJobs(96, 30, true), manyRuns(25, 60), 60)
	if job.jobsFocused() != projectPane || job.rCursor != 0 {
		t.Fatal("the job screen starts on its runs")
	}
	up := press(t, job, "up")
	if up.jobsFocused() != actionsPane || up.actKeys[jobView] != "esc" {
		t.Fatalf("up from the first run reaches the last action, got %q", up.actKeys[jobView])
	}
	if back := press(t, up, "enter"); back.view != jobsView {
		t.Fatal("Back to the jobs goes back")
	}
	if back := press(t, job, "esc"); back.view != jobsView {
		t.Fatal("esc goes back to the jobs")
	}

	out := press(t, job, "down", "enter")
	if out.view != logsView || out.logs.source.kind != "Run" || out.logs.source.run != 59 || out.logs.source.job != 1 {
		t.Fatalf("enter on a run opens its output: %+v", out.logs.source)
	}
	out.logStop()

	member := press(t, job, "n")
	if member.busy != "Starting nightly-cleanup" {
		t.Fatalf("n should start a run, busy %q", member.busy)
	}
	viaRow := press(t, job, "up", "up", "enter")
	if viaRow.busy != "Starting nightly-cleanup" {
		t.Fatal("Enter on Run now should start a run")
	}

	viewer := job
	viewer.role = "viewer"
	if labels(viewer.jobActions()) != "Back to the jobs (esc)" {
		t.Fatalf("a viewer isn't offered Run now: %s", labels(viewer.jobActions()))
	}
	if got := press(t, viewer, "n"); got.busy != "" || got.notice != "Viewers can't run jobs. Ask the owner to make you a member." {
		t.Fatalf("a viewer's n gets the role sentence, got %q", got.notice)
	}

	going := job
	going.jobs["landing"].Jobs[0].Running = true
	going.jobRuns[0].Status = "running"
	if labels(going.jobActions()) != "Show the run that's going (n), Back to the jobs (esc)" {
		t.Fatalf("a running job offers its run: %s", labels(going.jobActions()))
	}
	if got := press(t, going, "n"); got.view != logsView || got.logs.source.run != 60 || !got.logs.source.follow {
		t.Fatal("n on a running job follows its run")
	} else {
		got.logStop()
	}
	if !going.active() {
		t.Fatal("a running job makes the cockpit refresh every few seconds")
	}
	going.jobs["landing"].Jobs[0].Running = false
}

func TestRunsLoadAsYouScroll(t *testing.T) {
	job := onJob(t, withJobs(96, 30, true), manyRuns(25, 60), 60)
	if job.moreRuns() != nil {
		t.Fatal("no more runs are needed while the cursor is above the last one")
	}
	job = press(t, job, "G")
	if job.rCursor != 24 || !job.loadingRuns {
		t.Fatalf("the last run loaded should ask for the next page, at %d", job.rCursor)
	}
	job.keepRuns(manyRuns(50, 60)[25:], 60, false)
	if len(job.jobRuns) != 50 || job.jobRuns[25].ID != 35 || job.jobRuns[24].ID != 36 {
		t.Fatalf("the next page goes on the end: %d runs", len(job.jobRuns))
	}
	if job.jobRuns[job.rCursor].ID != 36 {
		t.Fatal("the cursor stays on its run")
	}
	newer := append([]jobRun{{ID: 61, Status: "running", StartedBy: "manual", StartedAt: time.Now()}}, manyRuns(25, 60)...)
	job.keepRuns(newer[:25], 61, true)
	if len(job.jobRuns) != 51 || job.jobRuns[0].ID != 61 || job.jobRuns[job.rCursor].ID != 36 {
		t.Fatalf("a refresh puts new runs first and keeps the older ones: %d, first %d", len(job.jobRuns), job.jobRuns[0].ID)
	}
}

func TestJobsTakeClicks(t *testing.T) {
	for _, size := range [][2]int{{96, 30}, {80, 24}} {
		m := withJobs(size[0], size[1], true)
		if got := clickAt(t, m, "Jobs (3)"); got.focused() != actionsPane || got.actKeys[homeView] != "J" {
			t.Fatalf("%v: a click on the Jobs row selects it", size)
		} else if got = clickAt(t, got, "Jobs (3)"); got.view != jobsView {
			t.Fatalf("%v: a second click opens the jobs", size)
		}
		jobs := press(t, m, "J")
		jobs = clickAt(t, jobs, "monthly-report")
		if jobs.jCursor != 2 || jobs.view != jobsView {
			t.Fatalf("%v: a click selects the job", size)
		}
		if opened := clickAt(t, jobs, "monthly-report"); opened.view != jobView || opened.jobID != 3 {
			t.Fatalf("%v: a click on the selected job opens it", size)
		}

		job := onJob(t, m, manyRuns(25, 60), 60)
		job = clickAt(t, job, "Run now by Ada")
		if job.rCursor != 1 || job.jobsFocused() != projectPane {
			t.Fatalf("%v: a click selects the run", size)
		}
		if out := clickAt(t, job, "Run now by Ada"); out.view != logsView || out.logs.source.run != 59 {
			t.Fatalf("%v: a click on the selected run opens its output", size)
		} else {
			out.logStop()
		}
		act := clickAt(t, job, "Run now  ")
		if act.jobsFocused() != actionsPane || act.actKeys[jobView] != "n" || act.busy != "" {
			t.Fatalf("%v: a click on Run now selects it first", size)
		}
		if ran := clickAt(t, act, "Run now  "); ran.busy != "Starting nightly-cleanup" {
			t.Fatalf("%v: a second click on Run now starts the run", size)
		}
		if w := wheel(t, job, "on its schedule", true); w.rCursor != 2 {
			t.Fatalf("%v: the wheel moves through the runs, at %d", size, w.rCursor)
		}
	}
}

func TestRunOutputIsColouredLikeTheJobPage(t *testing.T) {
	pier, out, errs := runRow(runLine{Stream: "pier", Text: "$ npm run cleanup"}), runRow(runLine{Stream: "stdout", Text: "removed 12"}), runRow(runLine{Stream: "stderr", Text: "oops"})
	if pier.tone != lavender || out.tone != pink || errs.tone != coral || errs.ink != coralTxt {
		t.Fatal("pier violet, the output pink, stderr coral")
	}
	failed := jobRun{Status: "failed", StartedAt: time.Now().Add(-2 * time.Second), FinishedAt: time.Now(), ExitCode: intp(3), Detail: "It exited with code 3."}
	if got := runEnd(failed, "").message; got != "Failed after 2.0s, exit 3: It exited with code 3." {
		t.Fatalf("the end says how it went: %q", got)
	}
	if got := runEnd(failed, "It exited with code 3.").message; got != "Failed after 2.0s, exit 3" {
		t.Fatalf("the end doesn't repeat what pierd just said: %q", got)
	}
	path := jobRun{Status: "succeeded", StartedAt: time.Now().Add(-300 * time.Millisecond), FinishedAt: time.Now(), HTTPStatus: intp(200)}
	if got := runEnd(path, ""); got.message != "Succeeded in 300 ms, HTTP 200" || got.ink != mint {
		t.Fatalf("a path job's end: %q", got.message)
	}
}

// A run's output is read the way the job page reads it: from the start,
// asking for what's new each second until it's done.
func TestRunLogFollowsUntilDone(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/projects/landing/jobs/1/runs/7" {
			http.NotFound(w, r)
			return
		}
		switch calls.Add(1) {
		case 1:
			fmt.Fprint(w, `{"run":{"id":7,"status":"running"},"lines":[{"ms":0,"stream":"pier","text":"$ npm run cleanup"}],"next":1,"done":false}`)
		default:
			if r.URL.Query().Get("from") != "1" {
				t.Errorf("the second ask should start from line 1, not %s", r.URL.Query().Get("from"))
			}
			fmt.Fprint(w, `{"run":{"id":7,"status":"failed","exit_code":1,"started_at":"2026-11-22T10:00:00Z","finished_at":"2026-11-22T10:00:01Z","detail":"It exited with code 1."},"lines":[{"ms":900,"stream":"stderr","text":"could not reach the database"},{"ms":950,"stream":"pier","text":"It exited with code 1."}],"next":3,"done":true}`)
		}
	}))
	defer srv.Close()
	s := logSource{c: config{Server: srv.URL}, project: "landing", kind: "Run", job: 1, run: 7, follow: true}
	var got []logRow
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.read(ctx, func(rows []logRow) error { got = append(got, rows...); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || got[1].label != "stderr" || got[3].message != "Failed after 1.0s, exit 1" {
		t.Fatalf("rows: %+v", got)
	}
	if s.title() != "Run 7" {
		t.Fatalf("title %q", s.title())
	}
}

// Leaving a run's output goes back to the job and refreshes it, so the
// run just watched shows how it ended.
func TestBackFromARunRefreshesTheJob(t *testing.T) {
	job := onJob(t, withJobs(96, 30, true), manyRuns(3, 3), 3)
	out := press(t, job, "enter")
	next, cmd := out.Update(tea.KeyMsg{Type: tea.KeyEsc})
	back := next.(cockpit)
	if back.view != jobView || cmd == nil || !back.loading {
		t.Fatal("esc from a run goes back to its job and asks for its runs again")
	}
	if w := back.refreshWant(); w.job != 1 || w.runs != 3 {
		t.Fatalf("the refresh asks for the job's runs: %+v", w)
	}
}
