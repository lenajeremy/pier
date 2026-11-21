package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"pier/internal/store"
)

// commit is a commit ID made of one repeated hex digit.
func commit(digit string) string { return strings.Repeat(digit, 40) }

func TestGitHubRebuildsAfterPierdStops(t *testing.T) {
	env := newGitHubEnv(t)
	bg := context.Background()
	env.push("p1", "main", commit("b"))

	// pierd is told to stop while the build command runs.
	ctx, stop := context.WithCancel(bg)
	started := make(chan struct{})
	env.runner.build(func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	j := env.next(bg)
	done := make(chan error, 1)
	go func() { done <- env.a.workGitHub(ctx, &j) }()
	<-started
	stop()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("the stopped build returned %v", err)
	}
	cut := j.Deployment
	if state, dep, _ := env.job(j.ID); state != "queued" || dep != "" {
		t.Fatalf("the job is %s with deployment %q; want queued with none", state, dep)
	}
	if old, err := env.st.Deployment(bg, cut); err != nil || old.Status != store.Failed || old.Error != store.InterruptedBuild {
		t.Fatalf("the cut build is %+v, %v", old, err)
	}
	if got := env.hub.lastStatus(); got != "pending" {
		t.Fatalf("the stopped build was reported as %q", got)
	}

	// The next start builds the commit again and reports that build.
	env.runner.build(nil)
	again := env.run(bg)
	if again.Deployment == cut || env.hub.lastStatus() != "success" || env.aliases()["site.pier.test"] != again.Deployment {
		t.Fatalf("the commit wasn't rebuilt: %+v, status %s", again, env.hub.lastStatus())
	}
	env.idle()
}

func TestGitHubRebuildsWhatARestartLeft(t *testing.T) {
	bg := context.Background()
	// Each case is how a job's build can look when pierd starts again.
	for name, leave := range map[string]func(*ghEnv, int64, store.Deployment) error{
		"still building": func(*ghEnv, int64, store.Deployment) error { return nil },
		"application build failed at startup": func(env *ghEnv, _ int64, dep store.Deployment) error {
			return errors.Join(env.st.TrackApp(bg, dep.ID, "pier-app-test"), env.st.InterruptAppBuilds(bg))
		},
		"failed as pierd exited": func(env *ghEnv, job int64, dep store.Deployment) error {
			dep.Status, dep.Error = store.Failed, "make timed out after 0.1s"
			return errors.Join(env.st.FinishDeployment(bg, dep), env.st.NoteGitHubJob(bg, job, store.InterruptedBuild))
		},
	} {
		t.Run(name, func(t *testing.T) {
			env := newGitHubEnv(t)
			env.push("p1", "main", commit("b"))
			j := env.next(bg)
			dep := store.Deployment{ID: "0dead0", Project: "site", Branch: "main", Commit: j.SHA, Status: store.Building, Target: store.TargetProduction, Source: store.SourceGitHub}
			if err := errors.Join(env.st.StartGitHubDeployment(bg, j.ID, dep), leave(env, j.ID, dep)); err != nil {
				t.Fatal(err)
			}
			again := env.run(bg)
			if again.Deployment == dep.ID || env.hub.lastStatus() != "success" || env.aliases()["site.pier.test"] != again.Deployment {
				t.Fatalf("the commit wasn't rebuilt: %+v, status %s", again, env.hub.lastStatus())
			}
			if old, err := env.st.Deployment(bg, dep.ID); err != nil || old.Status != store.Failed || old.Error != store.InterruptedBuild {
				t.Fatalf("the interrupted build is %+v, %v", old, err)
			}
			if state, _, detail := env.job(j.ID); state != "done" || detail != "" {
				t.Fatalf("the job ended %s %q", state, detail)
			}
		})
	}

	// A build that failed by itself is reported, not built again.
	env := newGitHubEnv(t)
	env.push("p1", "main", commit("b"))
	j := env.next(bg)
	dep := store.Deployment{ID: "0dead0", Project: "site", Branch: "main", Commit: j.SHA, Status: store.Building, Target: store.TargetProduction, Source: store.SourceGitHub}
	err := env.st.StartGitHubDeployment(bg, j.ID, dep)
	dep.Status, dep.Error = store.Failed, "make failed: exit status 2"
	if err = errors.Join(err, env.st.FinishDeployment(bg, dep)); err != nil {
		t.Fatal(err)
	}
	builds := env.runner.count()
	if again := env.run(bg); again.Deployment != dep.ID || env.hub.lastStatus() != "failure" || env.runner.count() != builds {
		t.Fatalf("a failed build was rebuilt: %+v, status %s", again, env.hub.lastStatus())
	}
}

func TestGitHubDisconnect(t *testing.T) {
	env := newGitHubEnv(t)
	bg := context.Background()
	first := env.secret
	env.push("p1", "feature", commit("b"))
	env.pull("pr1", "opened", commit("c"), true)
	if code := env.event("push", "p0", `{"installation":{"id":42},"repository":{"id":7},"ref":"refs/heads/old","deleted":true}`); code != 202 {
		t.Fatalf("branch deletion answered %d", code)
	}

	if code := env.call("DELETE", "/api/github", "", nil).Code; code != 401 {
		t.Fatalf("anonymous disconnect answered %d", code)
	}
	if w := env.call("DELETE", "/api/github", "", ownerHeaders); w.Code != 200 || !strings.Contains(w.Body.String(), `"dismissed":2`) {
		t.Fatalf("disconnect answered %d %s", w.Code, w.Body)
	}
	if info := env.call("GET", "/api/github", "", ownerHeaders).Body.String(); !strings.Contains(info, `"slug":""`) || !strings.Contains(info, `"jobs":[]`) {
		t.Fatalf("still connected: %s", info)
	}
	// Only the job that needs no App is left, and it still runs.
	if j := env.run(bg); j.SHA != "" {
		t.Fatalf("a build survived the disconnect: %+v", j)
	}
	env.idle()
	if sealed, err := env.st.Setting(bg, appSetting); err != nil || sealed != "" {
		t.Fatalf("the App is still stored: %v", err)
	}
	if project, err := env.st.GitHubProject(bg, 7); err != nil || project != "site" {
		t.Fatalf("the project lost its repository: %q, %v", project, err)
	}
	if env.aliases()["site.pier.test"] == "" {
		t.Fatal("production stopped serving")
	}
	push := fmt.Sprintf(`{"installation":{"id":43},"repository":{"id":7},"ref":"refs/heads/main","after":%q}`, commit("d"))
	if code := env.event("push", "p2", push); code != 401 {
		t.Fatalf("a delivery signed by the old App answered %d", code)
	}

	// The manifest flow runs again, and the new App's deliveries deploy.
	env.connect()
	if env.secret == first {
		t.Fatal("the new App has the old secret")
	}
	env.hub.heads["main"] = commit("d")
	if code := env.event("push", "p2", push); code != 202 {
		t.Fatalf("the new App's delivery answered %d", code)
	}
	env.run(bg)
	if env.hub.lastStatus() != "success" || env.hub.tokens[len(env.hub.tokens)-1] != "43" {
		t.Fatalf("status %s, tokens %v", env.hub.lastStatus(), env.hub.tokens)
	}
}

func TestGitHubFollowsTheInstallation(t *testing.T) {
	env := newGitHubEnv(t)
	bg := context.Background()
	// The App was uninstalled and installed again, which gave it a new ID.
	env.hub.heads["main"] = commit("b")
	moved := fmt.Sprintf(`{"installation":{"id":43},"repository":{"id":7},"ref":"refs/heads/main","after":%q}`, commit("b"))
	if code := env.event("push", "p1", moved); code != 202 {
		t.Fatalf("the delivery answered %d", code)
	}
	if j := env.run(bg); j.Installation != 43 || env.hub.tokens[len(env.hub.tokens)-1] != "43" || env.hub.lastStatus() != "success" {
		t.Fatalf("job %+v, tokens %v, status %s", j, env.hub.tokens, env.hub.lastStatus())
	}

	// A repository the App can see but no project deploys is said to be so.
	unused := fmt.Sprintf(`{"installation":{"id":43},"repository":{"id":99,"full_name":"owner/other"},"ref":"refs/heads/main","after":%q}`, commit("c"))
	if code := env.event("push", "p2", unused); code != 204 {
		t.Fatalf("a delivery for an unused repository answered %d", code)
	}
	if log := env.logs.String(); !strings.Contains(log, "no project uses this repository") || !strings.Contains(log, "owner/other") {
		t.Fatalf("the unused repository isn't in the log: %s", log)
	}
	env.idle()
}

func TestGitHubRetriesTemporaryFailures(t *testing.T) {
	waits := githubRetry
	githubRetry = []time.Duration{0, 0}
	t.Cleanup(func() { githubRetry = waits })
	env := newGitHubEnv(t)
	bg := context.Background()
	// answer makes GitHub fail the chosen status reports, counted from 1.
	var reports int
	answer := func(code func(report int) int) {
		env.hub.mu.Lock()
		defer env.hub.mu.Unlock()
		reports = 0
		env.hub.fail = func(r *http.Request) int {
			if r.Method != "POST" || !strings.Contains(r.URL.Path, "/statuses/") {
				return 0
			}
			reports++
			return code(reports)
		}
	}

	// Two 502s while reporting the result: the build is reported, once.
	answer(func(report int) int {
		if report == 2 || report == 3 {
			return 502
		}
		return 0
	})
	env.push("p1", "main", commit("b"))
	j := env.next(bg)
	builds := env.runner.count()
	if err := env.a.tryGitHub(bg, &j); err != nil {
		t.Fatal(err)
	}
	if state, _, _ := env.job(j.ID); state != "done" || env.runner.count() != builds+1 || env.hub.lastStatus() != "success" || reports != 4 {
		t.Fatalf("job %s after %d builds and %d reports, status %s", state, env.runner.count()-builds, reports, env.hub.lastStatus())
	}

	// GitHub stays down: after the last try the job waits for the owner.
	answer(func(int) int { return 503 })
	env.push("p2", "main", commit("c"))
	ctx, stop := context.WithCancel(bg)
	stopped := make(chan struct{})
	go func() { env.a.RunGitHub(ctx); close(stopped) }()
	for deadline := time.Now().Add(10 * time.Second); len(env.waiting()) == 0 && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
	}
	stop()
	<-stopped
	if jobs := env.waiting(); len(jobs) != 1 || jobs[0].State != "error" || !strings.Contains(jobs[0].Detail, "503") || reports != len(githubRetry)+1 {
		t.Fatalf("jobs %+v after %d reports", jobs, reports)
	}

	// A refusal isn't tried again.
	answer(func(int) int { return 404 })
	env.push("p3", "main", commit("d"))
	j = env.next(bg)
	if err := env.a.tryGitHub(bg, &j); err == nil || reports != 1 {
		t.Fatalf("got %v after %d reports", err, reports)
	}
}

func TestTemporary(t *testing.T) {
	for _, c := range []struct {
		err  error
		want bool
	}{
		{&ghError{msg: "GitHub could not be reached"}, true},
		{&ghError{Status: 502}, true},
		{&ghError{Status: 429, Limited: true}, true},
		{&ghError{Status: 403, Limited: true}, true},
		{fmt.Errorf("posting status: %w", &ghError{Status: 503}), true},
		{&ghError{Status: 403}, false},
		{&ghError{Status: 404}, false},
		{&ghError{Status: 422}, false},
		{errors.New("the repository archive exceeds the upload limit"), false},
		{nil, false},
	} {
		if got := temporary(c.err); got != c.want {
			t.Errorf("temporary(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

func TestGitHubArchiveDownloadIsBoundedByProgress(t *testing.T) {
	stall := githubStall
	githubStall = 200 * time.Millisecond
	t.Cleanup(func() { githubStall = stall })
	env := newGitHubEnv(t)
	bg := context.Background()
	if githubArchive.Timeout != 0 {
		t.Fatal("archive downloads have a total timeout")
	}
	archive := siteArchive(commit("b"))
	serve := func(fn func(w http.ResponseWriter, r *http.Request)) {
		env.hub.mu.Lock()
		env.hub.tarball = fn
		env.hub.mu.Unlock()
	}

	// A slow download that keeps arriving may outlast the stall limit.
	serve(func(w http.ResponseWriter, r *http.Request) {
		for part := range slices.Chunk(archive, len(archive)/8+1) {
			w.Write(part)
			w.(http.Flusher).Flush()
			time.Sleep(40 * time.Millisecond)
		}
	})
	start := time.Now()
	path, err := env.a.downloadGitHub(bg, "token", "owner/site", commit("b"))
	if err != nil || time.Since(start) < githubStall {
		t.Fatalf("slow download: %v after %s", err, time.Since(start))
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, archive) {
		t.Fatalf("the archive arrived changed: %v", err)
	}

	// One that stops arriving ends, and is worth another try.
	serve(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive[:16])
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	start = time.Now()
	if _, err := env.a.downloadGitHub(bg, "token", "owner/site", commit("b")); !temporary(err) || time.Since(start) > 10*time.Second {
		t.Fatalf("stalled download: %v after %s", err, time.Since(start))
	}
}

func TestGitHubKeepsOnePreviewComment(t *testing.T) {
	env := newGitHubEnv(t)
	bg := context.Background()
	// Quoting Pier's marker doesn't make someone's comment Pier's.
	quote := "As Pier said: " + previewMarker
	env.hub.comments = []hubComment{{ID: 1, Body: quote}}
	env.pull("pr1", "opened", commit("b"), false)
	env.run(bg)
	env.pull("pr2", "synchronize", commit("c"), false)
	latest := env.run(bg)
	if env.hub.posted != 1 || env.hub.patched != 1 || len(env.hub.comments) != 2 {
		t.Fatalf("%d comments posted, %d rewritten: %+v", env.hub.posted, env.hub.patched, env.hub.comments)
	}
	body := env.hub.comments[1].Body
	if !strings.Contains(body, "`ccccccc`") || !strings.Contains(body, "site-"+latest.Deployment+".pier.test") || env.hub.comments[0].Body != quote {
		t.Fatalf("comments %+v", env.hub.comments)
	}
}

func TestGitHubRetiresPreviews(t *testing.T) {
	env := newGitHubEnv(t)
	bg := context.Background()
	const zero = "0000000000000000000000000000000000000000"
	deleted := func(id, branch string) {
		t.Helper()
		env.hub.mu.Lock()
		delete(env.hub.heads, branch)
		env.hub.mu.Unlock()
		body := fmt.Sprintf(`{"installation":{"id":42},"repository":{"id":7},"ref":"refs/heads/%s","after":%q,"deleted":true}`, branch, zero)
		if code := env.event("push", id, body); code != 202 {
			t.Fatalf("branch deletion answered %d", code)
		}
	}
	env.push("p1", "feature", commit("b"))
	branch := env.run(bg)
	env.push("p2", "pr-5", commit("b"))
	env.run(bg)
	env.pull("pr1", "opened", commit("c"), false)
	pr := env.run(bg)
	live := env.aliases()
	if live["feature--site.pier.test"] != branch.Deployment || live["pr-9--site.pier.test"] != pr.Deployment || live["branch-pr-5--site.pier.test"] == "" {
		t.Fatalf("previews %v", live)
	}

	// A push still queued when the pull request closes isn't built, and
	// then the address goes, from the store and from the router.
	env.pull("pr2", "synchronize", commit("d"), false)
	env.pull("pr3", "closed", commit("d"), false)
	stale := env.run(bg)
	if state, _, _ := env.job(stale.ID); state != "superseded" {
		t.Fatalf("the push to a closed pull request is %s", state)
	}
	env.run(bg)
	if _, ok := env.aliases()["pr-9--site.pier.test"]; ok || strings.Contains(env.routes.String(), "pr-9--site.pier.test") {
		t.Fatal("the closed pull request still has its address")
	}
	if !strings.Contains(env.routes.String(), "feature--site.pier.test") {
		t.Fatal("the router lost a preview that is still open")
	}

	// Deleted branches lose theirs too, under whichever name it was filed,
	// and a push that was waiting for a deleted branch isn't an error.
	env.push("p3", "feature", commit("e"))
	deleted("p4", "feature")
	deleted("p5", "pr-5")
	if stale = env.run(bg); len(env.waiting()) != 0 {
		t.Fatalf("the push to a deleted branch waits for the owner: %+v", env.waiting())
	}
	if state, _, _ := env.job(stale.ID); state != "superseded" {
		t.Fatalf("the push to a deleted branch is %s", state)
	}
	env.run(bg)
	env.run(bg)
	live = env.aliases()
	if len(live) != 1 || live["site.pier.test"] == "" || strings.Contains(env.routes.String(), "--site.pier.test") {
		t.Fatalf("addresses left: %v", live)
	}

	// Production's address is not a preview, even if its branch is deleted.
	deleted("p6", "main")
	env.run(bg)
	if env.aliases()["site.pier.test"] == "" {
		t.Fatal("production lost its address")
	}
	// The deployment stays, with its own address, until preview cleanup.
	if _, err := env.st.Deployment(bg, pr.Deployment); err != nil {
		t.Fatal(err)
	}
	env.idle()
}

func TestGitHubAcceptsDeliveriesUpToGitHubsLimit(t *testing.T) {
	env := newGitHubEnv(t)
	env.hub.heads["feature"] = commit("b")
	push := func(padding int) string {
		return fmt.Sprintf(`{"installation":{"id":42},"repository":{"id":7},"ref":"refs/heads/feature","after":%q,"commits":[{"message":%q}]}`, commit("b"), strings.Repeat("x", padding))
	}
	if code := env.event("push", "large", push(6<<20)); code != 202 {
		t.Fatalf("a 6 MB delivery answered %d", code)
	}
	if code := env.event("push", "too-large", push(maxDelivery)); code != 413 {
		t.Fatalf("a delivery over the limit answered %d", code)
	}
}

func TestGitHubApprovalJobs(t *testing.T) {
	env := newGitHubEnv(t)
	bg := context.Background()
	// Each push to a fork's pull request replaces the one waiting before it.
	env.pull("pr1", "opened", commit("b"), true)
	first := env.waiting()[0]
	env.pull("pr2", "synchronize", commit("c"), true)
	env.pull("pr1", "opened", commit("b"), true) // GitHub sends pr1 again
	jobs := env.waiting()
	if len(jobs) != 1 || jobs[0].SHA != commit("c") {
		t.Fatalf("waiting for approval: %+v", jobs)
	}
	if state, _, _ := env.job(first.ID); state != "superseded" {
		t.Fatalf("the older commit is %s", state)
	}

	// The owner can turn the newest one down.
	path := fmt.Sprintf("/api/github/jobs/%d/dismiss", jobs[0].ID)
	if code := env.call("POST", path, "", nil).Code; code != 401 {
		t.Fatalf("anonymous dismissal answered %d", code)
	}
	if w := env.call("POST", path, "", ownerHeaders); w.Code != 200 {
		t.Fatalf("dismissal answered %d %s", w.Code, w.Body)
	}
	if state, _, _ := env.job(jobs[0].ID); state != "dismissed" || len(env.waiting()) != 0 {
		t.Fatalf("the dismissed job is %s", state)
	}
	for _, again := range []string{path, fmt.Sprintf("/api/github/jobs/%d/resume", jobs[0].ID)} {
		if code := env.call("POST", again, "", ownerHeaders).Code; code != 409 {
			t.Fatalf("%s on a dismissed job answered %d", again, code)
		}
	}

	// Closing the pull request ends the wait as well.
	env.pull("pr3", "synchronize", commit("d"), true)
	env.pull("pr4", "closed", commit("d"), true)
	if jobs := env.waiting(); len(jobs) != 0 {
		t.Fatalf("still waiting after the pull request closed: %+v", jobs)
	}
	env.run(bg)
	env.idle()
}

func TestGitHubGivesUpOnABuildThatKeepsStoppingPierd(t *testing.T) {
	env := newGitHubEnv(t)
	bg := context.Background()
	env.push("p1", "main", commit("b"))
	// stopDuring starts the queued build and stops pierd while it runs.
	stopDuring := func() int64 {
		t.Helper()
		ctx, stop := context.WithCancel(bg)
		started := make(chan struct{})
		env.runner.build(func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		})
		j := env.next(bg)
		done := make(chan error, 1)
		go func() { done <- env.a.workGitHub(ctx, &j) }()
		<-started
		stop()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("the stopped build returned %v", err)
		}
		return j.ID
	}

	// Twice pierd stops during the build, and the commit waits to build again.
	var job int64
	for range 2 {
		job = stopDuring()
		if state, dep, detail := env.job(job); state != "queued" || dep != "" || detail != "" {
			t.Fatalf("the job is %s with deployment %q and detail %q", state, dep, detail)
		}
	}
	// The third time the build takes pierd down with it, so nothing unwinds
	// and its record still says building. The next start doesn't build it.
	j := env.next(bg)
	cut := store.Deployment{ID: "0dead0", Project: "site", Branch: "main", Commit: j.SHA, Status: store.Building, Target: store.TargetProduction, Source: store.SourceGitHub}
	if err := env.st.StartGitHubDeployment(bg, j.ID, cut); err != nil {
		t.Fatal(err)
	}
	env.runner.build(nil)
	builds := env.runner.count()
	if j = env.next(bg); j.Deployment != cut.ID {
		t.Fatalf("the next start reads %+v", j)
	}
	if err := env.a.workGitHub(bg, &j); err != nil {
		t.Fatal(err)
	}
	if env.runner.count() != builds {
		t.Fatal("the commit was built a fourth time")
	}
	if old, err := env.st.Deployment(bg, cut.ID); err != nil || old.Status != store.Failed || old.Error != store.InterruptedBuild {
		t.Fatalf("the cut build is %+v, %v", old, err)
	}
	env.idle()
	if jobs := env.waiting(); len(jobs) != 1 || jobs[0].ID != job || jobs[0].State != "error" || jobs[0].Detail != store.UnsteadyBuild {
		t.Fatalf("waiting for the owner: %+v", jobs)
	}

	// Try again starts the count over, so one more stop doesn't end it.
	if w := env.call("POST", fmt.Sprintf("/api/github/jobs/%d/resume", job), "", ownerHeaders); w.Code != 200 {
		t.Fatalf("try again answered %d %s", w.Code, w.Body)
	}
	stopDuring()
	if state, _, _ := env.job(job); state != "queued" {
		t.Fatalf("after trying again and one stop the job is %s", state)
	}
	env.runner.build(nil)
	if built := env.run(bg); built.ID != job || env.hub.lastStatus() != "success" || env.aliases()["site.pier.test"] != built.Deployment {
		t.Fatalf("the commit wasn't built: %+v, status %s", built, env.hub.lastStatus())
	}
	if state, _, _ := env.job(job); state != "done" {
		t.Fatalf("the job ended %s", state)
	}
	env.idle()
}

func TestGitHubApprovalFollowsGitHubsOrder(t *testing.T) {
	env := newGitHubEnv(t)
	bg := context.Background()
	// forkPush delivers a fork's push to pull request 9 that GitHub made at
	// the given minute.
	forkPush := func(id, sha string, minute int) {
		t.Helper()
		body := fmt.Sprintf(`{"action":"synchronize","number":9,"installation":{"id":42},"repository":{"id":7},"pull_request":{"updated_at":"2026-11-20T10:%02d:00Z","base":{"repo":{"id":7}},"head":{"sha":%q,"repo":{"id":8}}}}`, minute, sha)
		if code := env.event("pull_request", id, body); code != 202 {
			t.Fatalf("pull request event answered %d", code)
		}
	}
	// The pull request is at its second commit, and GitHub delivers that
	// push before the first.
	env.hub.mu.Lock()
	env.hub.heads["pr-9"] = commit("c")
	env.hub.mu.Unlock()
	forkPush("pr2", commit("c"), 2)
	forkPush("pr1", commit("b"), 1)
	jobs := env.waiting()
	if len(jobs) != 1 || jobs[0].SHA != commit("c") {
		t.Fatalf("waiting for approval: %+v", jobs)
	}
	var late int64
	if err := env.db.QueryRow("SELECT id FROM github_jobs WHERE delivery='pr1'").Scan(&late); err != nil {
		t.Fatal(err)
	}
	if state, _, detail := env.job(late); state != "superseded" || detail != "A newer commit was pushed to this pull request." {
		t.Fatalf("the late delivery is %s %q", state, detail)
	}

	// Approved, the newer commit builds and serves the preview.
	if w := env.call("POST", fmt.Sprintf("/api/github/jobs/%d/resume", jobs[0].ID), "", ownerHeaders); w.Code != 200 {
		t.Fatalf("approval answered %d %s", w.Code, w.Body)
	}
	built := env.run(bg)
	if state, _, _ := env.job(built.ID); state != "done" || built.SHA != commit("c") || env.hub.lastStatus() != "success" || env.aliases()["pr-9--site.pier.test"] != built.Deployment {
		t.Fatalf("job %+v ended %s, status %s", built, state, env.hub.lastStatus())
	}
	env.idle()
}

func TestGitHubUnsignedDeliveriesStaySmall(t *testing.T) {
	env := newGitHubEnv(t)
	const size = 20 << 20
	// Not even JSON: a body is only decoded once its signature matches.
	body := strings.Repeat("x", size)
	forged := map[string]string{"X-GitHub-Event": "push", "X-GitHub-Delivery": "forged", "X-Hub-Signature-256": "sha256=" + strings.Repeat("0", 64)}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	code := env.call("POST", "/github/events", body, forged).Code
	runtime.ReadMemStats(&after)
	if code != 401 {
		t.Fatalf("a forged delivery answered %d", code)
	}
	if grew := after.TotalAlloc - before.TotalAlloc; grew > size/8 {
		t.Fatalf("a forged %d MB delivery allocated %d KB", size>>20, grew>>10)
	}

	// Signed or not, nothing of a delivery stays on disk.
	env.hub.heads["feature"] = commit("b")
	signed := fmt.Sprintf(`{"installation":{"id":42},"repository":{"id":7},"ref":"refs/heads/feature","after":%q,"commits":[{"message":%q}]}`, commit("b"), strings.Repeat("x", 1<<20))
	if code := env.event("push", "signed", signed); code != 202 {
		t.Fatalf("a signed delivery answered %d", code)
	}
	files, err := os.ReadDir(env.a.Deployer.Tmp)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasPrefix(f.Name(), "delivery-") {
			t.Fatalf("a delivery was left in %s: %s", env.a.Deployer.Tmp, f.Name())
		}
	}
}
