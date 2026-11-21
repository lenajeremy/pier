package api

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// TestGitHubRepositoryWithSeveralProjects follows a repository that two
// projects deploy, site and docs, as a monorepo's apps would be.
func TestGitHubRepositoryWithSeveralProjects(t *testing.T) {
	env := newGitHubEnv(t)
	bg := context.Background()
	both := []string{"docs", "site"}

	// A repository another project deploys can be imported again under a new
	// name. A name that is taken can't.
	if w := env.call("POST", "/api/github/import", `{"name":"docs","repo":7,"installation":42,"build":{"command":"make","output":"."}}`, ownerHeaders); w.Code != 201 {
		t.Fatalf("importing a used repository answered %d %s", w.Code, w.Body)
	}
	if w := env.call("POST", "/api/github/import", `{"name":"site","repo":7,"installation":42}`, ownerHeaders); w.Code != 409 || !strings.Contains(w.Body.String(), "This project name is already in use.") {
		t.Fatalf("importing under a used name answered %d %s", w.Code, w.Body)
	}
	if j := env.run(bg); j.Project != "docs" || env.aliases()["docs.pier.test"] != j.Deployment {
		t.Fatalf("docs' first build: %+v", j)
	}
	env.idle()

	// The catalog names every project of a repository, and none of one no
	// project deploys.
	w := env.call("GET", "/api/github/repositories", "", ownerHeaders)
	var catalog struct {
		Repositories []struct {
			ID       int64
			Projects []string
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &catalog); err != nil || w.Code != 200 {
		t.Fatalf("catalog %d %s", w.Code, w.Body)
	}
	listed := map[int64][]string{}
	for _, r := range catalog.Repositories {
		listed[r.ID] = r.Projects
	}
	if unused, ok := listed[8]; !slices.Equal(listed[7], both) || !ok || len(unused) != 0 || !strings.Contains(w.Body.String(), `"projects":[]`) {
		t.Fatalf("catalog %s", w.Body)
	}

	// A push makes a job for each project, and each builds the commit and
	// reports it under its own name.
	env.push("p1", "main", commit("b"))
	reported := len(env.hub.contexts)
	built := map[string]string{}
	for range 2 {
		j := env.run(bg)
		built[j.Project] = j.Deployment
	}
	env.idle()
	contexts, states := env.hub.contexts[reported:], env.hub.statuses[reported:]
	if !slices.Equal(contexts, []string{"Pier / docs / main", "Pier / docs / main", "Pier / site / main", "Pier / site / main"}) || !slices.Equal(states, []string{"pending", "success", "pending", "success"}) {
		t.Fatalf("statuses %v under %v", states, contexts)
	}
	for _, project := range both {
		dep, err := env.st.Deployment(bg, built[project])
		if err != nil || dep.Project != project || dep.Commit != commit("b") || env.aliases()[project+".pier.test"] != dep.ID {
			t.Fatalf("%s built %+v, %v", project, dep, err)
		}
	}

	// A branch gets a preview in each, and deleting it retires both.
	env.push("p2", "feature", commit("b"))
	for range 2 {
		env.run(bg)
	}
	if live := env.aliases(); live["feature--docs.pier.test"] == "" || live["feature--site.pier.test"] == "" {
		t.Fatalf("branch previews %v", live)
	}
	env.hub.mu.Lock()
	delete(env.hub.heads, "feature")
	env.hub.mu.Unlock()
	if code := env.event("push", "p3", `{"installation":{"id":42},"repository":{"id":7},"ref":"refs/heads/feature","deleted":true}`); code != 202 {
		t.Fatalf("branch deletion answered %d", code)
	}
	for range 2 {
		env.run(bg)
	}
	env.idle()
	if live := env.aliases(); live["feature--docs.pier.test"] != "" || live["feature--site.pier.test"] != "" || strings.Contains(env.routes.String(), "feature--") {
		t.Fatalf("addresses after the deletion: %v", live)
	}

	// A fork's pull request waits for the owner once, for both projects.
	env.pull("pr1", "opened", commit("c"), true)
	var info struct {
		Jobs []struct {
			ID       int64
			Project  string
			SHA      string
			State    string
			Projects []string
		}
	}
	if w := env.call("GET", "/api/github", "", ownerHeaders); json.Unmarshal(w.Body.Bytes(), &info) != nil || len(info.Jobs) != 1 {
		t.Fatalf("waiting: %d %s", w.Code, w.Body)
	}
	if wait := info.Jobs[0]; wait.State != "approval" || wait.SHA != commit("c") || wait.Project != "docs" || !slices.Equal(wait.Projects, both) {
		t.Fatalf("waiting: %+v", wait)
	}

	// Approving it queues the commit for both. The pull request keeps one
	// comment, and once both are built it lists both previews.
	if w := env.call("POST", fmt.Sprintf("/api/github/jobs/%d/resume", info.Jobs[0].ID), "", ownerHeaders); w.Code != 200 {
		t.Fatalf("approval answered %d %s", w.Code, w.Body)
	}
	for range 2 {
		env.run(bg)
	}
	env.idle()
	if jobs := env.waiting(); len(jobs) != 0 {
		t.Fatalf("still waiting after approval: %+v", jobs)
	}
	if env.hub.posted != 1 || env.hub.patched != 1 || len(env.hub.comments) != 1 {
		t.Fatalf("%d comments posted, %d rewritten: %+v", env.hub.posted, env.hub.patched, env.hub.comments)
	}
	body := env.hub.comments[0].Body
	for _, line := range []string{"- docs: https://pr-9--docs.pier.test (`ccccccc`)", "- site: https://pr-9--site.pier.test (`ccccccc`)"} {
		if !strings.Contains(body, line) {
			t.Fatalf("the comment lacks %q:\n%s", line, body)
		}
	}

	// The next commit waits once too, and turning it down turns it down for
	// both.
	env.pull("pr2", "synchronize", commit("d"), true)
	jobs := env.waiting()
	if len(jobs) != 1 || jobs[0].SHA != commit("d") || !slices.Equal(jobs[0].Projects, both) {
		t.Fatalf("waiting: %+v", jobs)
	}
	path := fmt.Sprintf("/api/github/jobs/%d/dismiss", jobs[0].ID)
	if w := env.call("POST", path, "", ownerHeaders); w.Code != 200 {
		t.Fatalf("dismissal answered %d %s", w.Code, w.Body)
	}
	var dismissed int
	if err := env.db.QueryRow("SELECT count(*) FROM github_jobs WHERE delivery='pr2' AND state='dismissed'").Scan(&dismissed); err != nil || dismissed != 2 {
		t.Fatalf("%d jobs dismissed, %v", dismissed, err)
	}
	if code := env.call("POST", path, "", ownerHeaders).Code; code != 409 {
		t.Fatalf("dismissing again answered %d", code)
	}

	// Closing the pull request retires both previews, and leaves production
	// and the one comment.
	env.pull("pr3", "closed", commit("d"), true)
	for range 2 {
		env.run(bg)
	}
	env.idle()
	live := env.aliases()
	for _, project := range both {
		if _, ok := live["pr-9--"+project+".pier.test"]; ok || live[project+".pier.test"] == "" {
			t.Fatalf("addresses after the close: %v", live)
		}
	}
	if strings.Contains(env.routes.String(), "pr-9--") || len(env.hub.comments) != 1 {
		t.Fatalf("routes %s, comments %+v", env.routes.String(), env.hub.comments)
	}
}
