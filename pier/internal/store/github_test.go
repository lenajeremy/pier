package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

// githubStore is a store with one project, site, imported from repository 7,
// and its import job still queued.
func githubStore(t *testing.T) *Store {
	t.Helper()
	s := open(t)
	if err := s.ImportGitHub(context.Background(), Project{Name: "site", ProductionBranch: "main"}, 7, 42, strings.Repeat("a", 40)); err != nil {
		t.Fatal(err)
	}
	return s
}

// githubJob reads the state and detail of the job a delivery made.
func githubJob(t *testing.T, s *Store, delivery string) (state, detail string) {
	t.Helper()
	if err := s.db.QueryRow("SELECT state,detail FROM github_jobs WHERE delivery=?", delivery).Scan(&state, &detail); err != nil {
		t.Fatal(delivery, err)
	}
	return
}

func TestGitHubApprovalsFollowGitHubsTime(t *testing.T) {
	const (
		newer  = "A newer commit was pushed to this pull request."
		closed = "The pull request was closed."
	)
	// A delivery for pull request 9 from a fork, made by GitHub at minute;
	// a negative minute is a delivery that doesn't say. No commit closes it.
	type delivery struct {
		id, sha string
		minute  int
	}
	for _, c := range []struct {
		name       string
		deliveries []delivery
		want       map[string][2]string // a delivery's state and detail
	}{
		{"in order", []delivery{{"d1", "b", 1}, {"d2", "c", 2}},
			map[string][2]string{"d1": {"superseded", newer}, "d2": {"approval", ""}}},
		{"the older push arrives late", []delivery{{"d2", "c", 2}, {"d1", "b", 1}},
			map[string][2]string{"d1": {"superseded", newer}, "d2": {"approval", ""}}},
		{"late behind two", []delivery{{"d2", "c", 2}, {"d3", "d", 3}, {"d1", "b", 1}},
			map[string][2]string{"d1": {"superseded", newer}, "d2": {"superseded", newer}, "d3": {"approval", ""}}},
		{"the same second goes by arrival", []delivery{{"d2", "c", 1}, {"d1", "b", 1}},
			map[string][2]string{"d1": {"approval", ""}, "d2": {"superseded", newer}}},
		{"no time goes by arrival", []delivery{{"d2", "c", -1}, {"d1", "b", -1}},
			map[string][2]string{"d1": {"approval", ""}, "d2": {"superseded", newer}}},
		{"a time beats none", []delivery{{"d2", "c", 2}, {"d1", "b", -1}},
			map[string][2]string{"d1": {"superseded", newer}, "d2": {"approval", ""}}},
		{"a close ends every wait, whatever its time", []delivery{{"d2", "c", 2}, {"d0", "", 1}},
			map[string][2]string{"d2": {"superseded", closed}, "d0": {"queued", ""}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			s := githubStore(t)
			for _, d := range c.deliveries {
				var at time.Time
				if d.minute >= 0 {
					at = time.Date(2026, 11, 20, 10, d.minute, 0, 0, time.UTC)
				}
				sha := ""
				if d.sha != "" {
					sha = strings.Repeat(d.sha, 40)
				}
				if projects, err := s.QueueGitHub(ctx, 7, 42, d.id, "pr-9", sha, 9, sha != "", at); err != nil || !slices.Equal(projects, []string{"site"}) {
					t.Fatalf("queue %s: %q, %v", d.id, projects, err)
				}
			}
			for id, want := range c.want {
				if state, detail := githubJob(t, s, id); state != want[0] || detail != want[1] {
					t.Errorf("%s is %s %q; want %s %q", id, state, detail, want[0], want[1])
				}
			}
		})
	}

	// A late one also loses to newer news the owner already answered: a
	// commit that was approved, or the pull request closing.
	ctx := context.Background()
	minute := func(m int) time.Time { return time.Date(2026, 11, 20, 10, m, 0, 0, time.UTC) }
	s := githubStore(t)
	if _, err := s.QueueGitHub(ctx, 7, 42, "d2", "pr-9", strings.Repeat("c", 40), 9, true, minute(2)); err != nil {
		t.Fatal(err)
	}
	var approved int64
	if err := s.db.QueryRowContext(ctx, "SELECT id FROM github_jobs WHERE delivery='d2'").Scan(&approved); err != nil {
		t.Fatal(err)
	}
	if err := s.ResumeGitHub(ctx, approved); err != nil {
		t.Fatal(err)
	}
	if _, err := s.QueueGitHub(ctx, 7, 42, "d1", "pr-9", strings.Repeat("b", 40), 9, true, minute(1)); err != nil {
		t.Fatal(err)
	}
	if state, detail := githubJob(t, s, "d1"); state != "superseded" || detail != newer {
		t.Errorf("late behind an approved commit: %s %q", state, detail)
	}
	if _, err := s.QueueGitHub(ctx, 7, 42, "d9", "pr-9", "", 9, false, minute(9)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.QueueGitHub(ctx, 7, 42, "d3", "pr-9", strings.Repeat("d", 40), 9, true, minute(3)); err != nil {
		t.Fatal(err)
	}
	if state, detail := githubJob(t, s, "d3"); state != "superseded" || detail != closed {
		t.Errorf("late after the close: %s %q", state, detail)
	}

	// A push carries no time and supersedes nothing, as before.
	s = githubStore(t)
	for _, id := range []string{"p1", "p2"} {
		if _, err := s.QueueGitHub(ctx, 7, 42, id, "feature", strings.Repeat("b", 40), 0, false, time.Time{}); err != nil {
			t.Fatal(err)
		}
		if state, _ := githubJob(t, s, id); state != "queued" {
			t.Fatalf("push %s is %s", id, state)
		}
	}
}

func TestGitHubGivesUpOnABuildPierdKeepsStoppingDuring(t *testing.T) {
	ctx := context.Background()
	s := githubStore(t)
	j, err := s.NextGitHub(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// interrupt binds a building record to the job, as a build does, and
	// records that pierd stopped during it.
	interrupt := func(id string) bool {
		t.Helper()
		dep := Deployment{ID: id, Project: "site", Branch: "main", Commit: j.SHA, Status: Building, Target: TargetProduction, Source: SourceGitHub}
		if err := s.StartGitHubDeployment(ctx, j.ID, dep); err != nil {
			t.Fatal(err)
		}
		gaveUp, err := s.InterruptGitHubBuild(ctx, j.ID, id)
		if err != nil {
			t.Fatal(err)
		}
		if old, err := s.Deployment(ctx, id); err != nil || old.Status != Failed || old.Error != InterruptedBuild {
			t.Fatalf("the cut build is %+v, %v", old, err)
		}
		return gaveUp
	}

	// The first two stops leave the commit queued to build again.
	for _, id := range []string{"00000a", "00000b"} {
		if interrupt(id) {
			t.Fatalf("gave up after the stop during %s", id)
		}
		if state, detail := githubJob(t, s, "import"); state != "queued" || detail != "" {
			t.Fatalf("after the stop during %s the job is %s %q", id, state, detail)
		}
	}
	// The third waits for the owner, without its build.
	if !interrupt("00000c") {
		t.Fatal("still trying after three stops")
	}
	if _, err := s.NextGitHub(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("the job is still queued: %v", err)
	}
	waiting, err := s.GitHubAttention(ctx)
	if err != nil || len(waiting) != 1 || waiting[0].ID != j.ID || waiting[0].State != "error" || waiting[0].Detail != UnsteadyBuild || waiting[0].Deployment != "" {
		t.Fatalf("waiting %+v, %v", waiting, err)
	}

	// Trying again starts the count over: the next stop queues it again.
	if err := s.ResumeGitHub(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	if interrupt("00000d") {
		t.Fatal("gave up on the first stop after trying again")
	}
	if state, _ := githubJob(t, s, "import"); state != "queued" {
		t.Fatalf("after trying again the job is %s", state)
	}

	// A job answered during its build keeps the answer.
	if err := s.SaveGitHubJob(ctx, j.ID, "superseded", "", "The branch or pull request has moved."); err != nil {
		t.Fatal(err)
	}
	if gaveUp, err := s.InterruptGitHubBuild(ctx, j.ID, ""); err != nil || gaveUp {
		t.Fatalf("interrupting a finished job: %v, %v", gaveUp, err)
	}
	if state, _ := githubJob(t, s, "import"); state != "superseded" {
		t.Fatalf("the finished job is %s", state)
	}
}
