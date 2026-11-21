package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// jobStates counts a delivery's jobs by state.
func jobStates(t *testing.T, s *Store, delivery string) map[string]int {
	t.Helper()
	rows, err := s.db.Query("SELECT state,count(*) FROM github_jobs WHERE delivery=? GROUP BY state", delivery)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	states := map[string]int{}
	for rows.Next() {
		var state string
		var n int
		if err := rows.Scan(&state, &n); err != nil {
			t.Fatal(err)
		}
		states[state] = n
	}
	return states
}

func TestGitHubRepositoryWithSeveralProjects(t *testing.T) {
	ctx := context.Background()
	s := githubStore(t)
	both := []string{"docs", "site"}
	fail := func(id int64) {
		t.Helper()
		if err := s.SaveGitHubJob(ctx, id, "error", "", "GitHub returned 503"); err != nil {
			t.Fatal(err)
		}
	}

	// A second project can deploy the same repository, but not take a name.
	if err := s.ImportGitHub(ctx, Project{Name: "docs", ProductionBranch: "main"}, 7, 43, strings.Repeat("a", 40)); err != nil {
		t.Fatal(err)
	}
	if err := s.ImportGitHub(ctx, Project{Name: "site", ProductionBranch: "main"}, 7, 43, strings.Repeat("a", 40)); !errors.Is(err, ErrExists) {
		t.Fatalf("importing under a used name: %v", err)
	}
	if projects, err := s.GitHubProjects(ctx, 7); err != nil || !slices.Equal(projects, both) {
		t.Fatalf("projects of the repository: %q, %v", projects, err)
	}
	if projects, err := s.GitHubProjects(ctx, 99); err != nil || projects == nil || len(projects) != 0 {
		t.Fatalf("projects of an unused repository: %#v, %v", projects, err)
	}

	// Each import is its own: failing both leaves two to answer.
	var imports []int64
	for range 2 {
		j, err := s.NextGitHub(ctx)
		if err != nil {
			t.Fatal(err)
		}
		imports = append(imports, j.ID)
		fail(j.ID)
	}
	waiting, err := s.GitHubAttention(ctx)
	if err != nil || len(waiting) != 2 || waiting[0].Project != "docs" || !slices.Equal(waiting[0].Projects, []string{"docs"}) || !slices.Equal(waiting[1].Projects, []string{"site"}) {
		t.Fatalf("waiting %+v, %v", waiting, err)
	}
	if err := s.DismissGitHub(ctx, imports[0]); err != nil {
		t.Fatal(err)
	}
	if j, err := s.GitHubAttention(ctx); err != nil || len(j) != 1 || j[0].ID != imports[1] {
		t.Fatalf("dismissing one import answered another: %+v, %v", j, err)
	}
	if err := s.DismissGitHub(ctx, imports[1]); err != nil {
		t.Fatal(err)
	}

	// A delivery makes a job for each project, under the installation it
	// names, and their errors wait as one.
	projects, err := s.QueueGitHub(ctx, 7, 44, "p1", "main", strings.Repeat("b", 40), 0, false, time.Time{})
	if err != nil || !slices.Equal(projects, both) {
		t.Fatalf("queued for %q, %v", projects, err)
	}
	var jobs []GitHubJob
	for range 2 {
		j, err := s.NextGitHub(ctx)
		if err != nil {
			t.Fatal(err)
		}
		jobs = append(jobs, j)
		fail(j.ID)
	}
	if jobs[0].Project != "docs" || jobs[1].Project != "site" || jobs[0].Installation != 44 || jobs[1].Installation != 44 {
		t.Fatalf("jobs %+v", jobs)
	}
	waiting, err = s.GitHubAttention(ctx)
	if err != nil || len(waiting) != 1 || waiting[0].ID != jobs[0].ID || waiting[0].Detail != "GitHub returned 503" || !slices.Equal(waiting[0].Projects, both) {
		t.Fatalf("waiting %+v, %v", waiting, err)
	}
	// Trying either again tries both.
	if err := s.ResumeGitHub(ctx, jobs[1].ID); err != nil {
		t.Fatal(err)
	}
	if states := jobStates(t, s, "p1"); states["queued"] != 2 {
		t.Fatalf("after trying again: %v", states)
	}
	for _, j := range jobs {
		if err := s.SaveGitHubJob(ctx, j.ID, "done", "", ""); err != nil {
			t.Fatal(err)
		}
	}

	// A fork's commits wait once each, and a newer one replaces the older
	// for every project.
	minute := func(m int) time.Time { return time.Date(2026, 11, 20, 10, m, 0, 0, time.UTC) }
	for i, sha := range []string{"c", "d"} {
		if _, err := s.QueueGitHub(ctx, 7, 44, "d"+sha, "pr-9", strings.Repeat(sha, 40), 9, true, minute(i)); err != nil {
			t.Fatal(err)
		}
	}
	if states := jobStates(t, s, "dc"); states["superseded"] != 2 {
		t.Fatalf("the older commit: %v", states)
	}
	waiting, err = s.GitHubAttention(ctx)
	if err != nil || len(waiting) != 1 || waiting[0].SHA != strings.Repeat("d", 40) || waiting[0].State != "approval" || !slices.Equal(waiting[0].Projects, both) {
		t.Fatalf("waiting %+v, %v", waiting, err)
	}
	if err := s.DismissGitHub(ctx, waiting[0].ID); err != nil {
		t.Fatal(err)
	}
	if states := jobStates(t, s, "dd"); states["dismissed"] != 2 {
		t.Fatalf("after dismissing: %v", states)
	}
	if err := s.ResumeGitHub(ctx, waiting[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("approving a dismissed delivery: %v", err)
	}
}

func TestBranchPreview(t *testing.T) {
	ctx := context.Background()
	s := githubStore(t)
	build := func(id, branch, status, source string) {
		t.Helper()
		d := Deployment{ID: id, Project: "site", Branch: branch, Commit: strings.Repeat("b", 40), Status: Building, Target: TargetPreview, Source: source, CreatedAt: time.UnixMilli(int64(len(id)) * 1000)}
		if err := s.CreateDeployment(ctx, d); err != nil {
			t.Fatal(err)
		}
		d.Status = status
		if err := s.FinishDeployment(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	const host = "pr-9--site.pier.test"
	if _, _, err := s.BranchPreview(ctx, "site", "pr-9", host); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a branch with no build: %v", err)
	}

	// Without its address, a branch's preview is its newest ready build from
	// GitHub.
	build("0a", "pr-9", Ready, SourceGitHub)
	build("0bb", "pr-9", Failed, SourceGitHub)
	build("0ccc", "pr-9", Ready, SourceCLI)
	if d, serving, err := s.BranchPreview(ctx, "site", "pr-9", host); err != nil || serving || d.ID != "0a" {
		t.Fatalf("without the address: %s, %v, %v", d.ID, serving, err)
	}

	// The address wins when it serves.
	build("0dddd", "pr-9", Ready, SourceGitHub)
	if err := s.SetAlias(ctx, Alias{Host: host, Project: "site", Kind: KindBranch, Branch: "pr-9", DeploymentID: "0a"}); err != nil {
		t.Fatal(err)
	}
	if d, serving, err := s.BranchPreview(ctx, "site", "pr-9", host); err != nil || !serving || d.ID != "0a" {
		t.Fatalf("with the address: %s, %v, %v", d.ID, serving, err)
	}
}

// TestGitHubRepositoriesMigrate opens a database from before a repository
// could hold several projects.
func TestGitHubRepositoriesMigrate(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "pier.db")
	at := slices.IndexFunc(migrations, func(m string) bool { return strings.Contains(m, "github_repos_new") })
	if at < 0 {
		t.Fatal("no migration rebuilds github_repos")
	}
	old, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	old.SetMaxOpenConns(1)
	for _, stmt := range append([]string{schema}, migrations[:at]...) {
		if _, err := old.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	for _, stmt := range []string{
		"INSERT INTO projects(name,created_at) VALUES('site',1)",
		"INSERT INTO github_repos(project,repo_id,installation) VALUES('site',7,42)",
		"INSERT INTO github_jobs(id,project,delivery,branch,sha,pr,state,deployment,detail,interrupted,pr_updated) VALUES(5,'site','d1','pr-9','" + strings.Repeat("b", 40) + "',9,'error','0a','GitHub returned 503',2,1000)",
		fmt.Sprintf("PRAGMA user_version=%d", at),
	} {
		if _, err := old.Exec(stmt); err != nil {
			t.Fatal(stmt, err)
		}
	}
	old.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	waiting, err := s.GitHubAttention(ctx)
	if err != nil || len(waiting) != 1 {
		t.Fatalf("waiting %+v, %v", waiting, err)
	}
	if j := waiting[0]; j.ID != 5 || j.RepoID != 7 || j.Installation != 42 || j.Deployment != "0a" || j.Detail != "GitHub returned 503" {
		t.Fatalf("the job came through as %+v", j)
	}
	var interrupted, updated int
	if err := s.db.QueryRow("SELECT interrupted,pr_updated FROM github_jobs WHERE id=5").Scan(&interrupted, &updated); err != nil || interrupted != 2 || updated != 1000 {
		t.Fatalf("interrupted %d, pr_updated %d, %v", interrupted, updated, err)
	}
	if err := s.ImportGitHub(ctx, Project{Name: "docs", ProductionBranch: "main"}, 7, 42, strings.Repeat("a", 40)); err != nil {
		t.Fatalf("a second project on the repository: %v", err)
	}
	var violations, indexes int
	if err := s.db.QueryRow("SELECT count(*) FROM pragma_foreign_key_check").Scan(&violations); err != nil || violations != 0 {
		t.Fatalf("%d foreign key violations, %v", violations, err)
	}
	if err := s.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='index' AND name IN ('github_repos_by_repo','github_jobs_state')").Scan(&indexes); err != nil || indexes != 2 {
		t.Fatalf("%d indexes, %v", indexes, err)
	}
}
