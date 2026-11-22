package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"pier/internal/auth"
	"pier/internal/deploy"
	"pier/internal/jobs"
	"pier/internal/secrets"
	"pier/internal/store"
)

// jobServer is an API with jobs, a browser signed in to it, and the
// project blog, whose production is a static site.
func jobServer(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "pier.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hash, _ := auth.HashPassword("correct horse battery")
	st.CreateUser(ctx, "owner@example.com", "Owner", hash)
	box, _ := secrets.Load(filepath.Join(t.TempDir(), "secret.key"))
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	d := &deploy.Deployer{Store: st, Domain: "pier.localhost", PierHost: "pier.localhost", ProductionBranch: "main", Secrets: box, URL: func(h string) string { return "http://" + h }, Log: log}
	st.CreateProject(ctx, store.Project{Name: "blog"})
	dep := store.Deployment{ID: "c19e77", Project: "blog", Branch: "main", Target: store.TargetProduction, Status: store.Building}
	st.CreateDeployment(ctx, dep)
	dep.Status = store.Ready
	st.FinishDeployment(ctx, dep)
	st.SetAlias(ctx, store.Alias{Host: "blog.pier.localhost", Project: "blog", Kind: store.KindProduction, Branch: "main", DeploymentID: dep.ID})
	a := &API{Store: st, Log: log, Deployer: d, Jobs: &jobs.Scheduler{Store: st, Deployer: d, Log: log, Logs: t.TempDir()}}
	srv := httptest.NewServer(a.Public())
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	if resp, _ := do(t, c, "POST", srv.URL+"/api/session", `{"email":"owner@example.com","password":"correct horse battery"}`, true); resp.StatusCode != 200 {
		t.Fatalf("sign in: %d", resp.StatusCode)
	}
	return srv, c
}

// A job is checked before it's kept, and each mistake points at its field.
func TestJobsAreChecked(t *testing.T) {
	srv, c := jobServer(t)
	jobsURL := srv.URL + "/api/projects/blog/jobs"
	for body, want := range map[string]string{
		`{"name":"no spaces please","kind":"path","schedule":"0 3 * * *","path":"/api/cron"}`:      `"field":"name"`,
		`{"name":"nightly","kind":"path","schedule":"0 25 * * *","path":"/api/cron"}`:              `"error":"The hour must be from 0 to 23, not \"25\".","field":"schedule"`,
		`{"name":"nightly","kind":"path","schedule":"0 3 * * *","path":"api/cron"}`:                `"field":"path"`,
		`{"name":"nightly","kind":"path","schedule":"0 3 * * *","path":"/x","method":"DELETE"}`:    `"field":"method"`,
		`{"name":"nightly","kind":"path","schedule":"0 3 * * *","path":"/x","timezone":"Mars"}`:    `"field":"timezone"`,
		`{"name":"nightly","kind":"command","schedule":"0 3 * * *","command":"npm run cleanup"}`:   `blog's production is a static site, so there's no image to run a command in. Call a path instead.","field":"kind"`,
		`{"name":"nightly","kind":"path","schedule":"0 3 * * *","path":"/x","timeout_minutes":90}`: `"field":"timeout_minutes"`,
	} {
		code, out := raw(t, c, "POST", jobsURL, body)
		if code != 400 || !strings.Contains(out, want) {
			t.Errorf("%s: %d %s; want %s", body, code, out, want)
		}
	}

	code, out := raw(t, c, "POST", jobsURL, `{"name":"nightly","kind":"path","schedule":"0 3 * * *","timezone":"Europe/London","method":"post","path":"/api/cron"}`)
	if code != 200 || !strings.Contains(out, `"description":"Every day at 03:00 Europe/London time"`) || !strings.Contains(out, `"method":"POST"`) ||
		!strings.Contains(out, `"timeout_s":900`) || !strings.Contains(out, `"next_run":"`) {
		t.Fatalf("add: %d %s", code, out)
	}
	if code, out := raw(t, c, "POST", jobsURL, `{"name":"nightly","kind":"path","schedule":"0 4 * * *","path":"/x"}`); code != 400 || !strings.Contains(out, "blog already has a job called nightly.") {
		t.Fatalf("a second job of one name: %d %s", code, out)
	}
	code, out = raw(t, c, "GET", jobsURL, "")
	if code != 200 || !strings.Contains(out, `"production":{"created_at"`) || !strings.Contains(out, `"server":false`) || !strings.Contains(out, `"secret_name":"CRON_SECRET"`) {
		t.Fatalf("list: %d %s", code, out)
	}
	if code, _ := raw(t, c, "DELETE", jobsURL+"/1", ""); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if code, _ := raw(t, c, "GET", jobsURL+"/1", ""); code != 404 {
		t.Fatalf("a deleted job: %d", code)
	}
}

// The schedule field's preview: the schedule in words, and its next runs.
func TestSchedulePreview(t *testing.T) {
	srv, c := jobServer(t)
	code, out := raw(t, c, "POST", srv.URL+"/api/jobs/schedule", `{"schedule":"30 9 * * 1-5"}`)
	if code != 200 || !strings.Contains(out, `"description":"At 09:30 UTC, Monday to Friday"`) || strings.Count(out, "T09:30:00Z") != 3 {
		t.Fatalf("preview: %d %s", code, out)
	}
	if code, out := raw(t, c, "POST", srv.URL+"/api/jobs/schedule", `{"schedule":"every day"}`); code != 400 || !strings.Contains(out, "five parts") {
		t.Fatalf("not a schedule: %d %s", code, out)
	}
}
