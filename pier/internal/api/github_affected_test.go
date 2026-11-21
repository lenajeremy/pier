package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// newMonorepoEnv is site built from apps/docs of workspaceFiles, whose docs
// use packages/ui, with production already built from commit b.
func newMonorepoEnv(t *testing.T) *ghEnv {
	t.Helper()
	env := newGitHubEnv(t)
	env.hub.mu.Lock()
	env.hub.tarball = func(w http.ResponseWriter, r *http.Request) {
		w.Write(sourceArchive("owner-site-0123456", workspaceFiles))
	}
	env.hub.compares = map[string]hubCompare{}
	env.hub.mu.Unlock()
	env.settings(`{"root":"apps/docs","command":"make","output":"."}`)
	// The import was built from the whole repository, which says nothing of
	// apps/docs, so this builds whatever changed.
	env.push("p1", "main", commit("b"))
	if j := env.run(context.Background()); j.Deployment == "" {
		t.Fatal("the first build from apps/docs was skipped")
	}
	return env
}

func (e *ghEnv) settings(build string) {
	e.t.Helper()
	if w := e.call("PUT", "/api/projects/site/settings", `{"build":`+build+`}`, ownerHeaders); w.Code != 200 {
		e.t.Fatal(w.Code, w.Body)
	}
}

func (e *ghEnv) compare(base, head, status string, files ...hubFile) {
	e.hub.mu.Lock()
	defer e.hub.mu.Unlock()
	e.hub.compares[base+"..."+head] = hubCompare{Status: status, Files: files}
}

func (e *ghEnv) lastDetail() string {
	e.hub.mu.Lock()
	defer e.hub.mu.Unlock()
	return e.hub.details[len(e.hub.details)-1]
}

func changed(names ...string) []hubFile {
	var files []hubFile
	for _, n := range names {
		files = append(files, hubFile{Name: n})
	}
	return files
}

const skippedStatus = "No changes for this project, so nothing was built."

// pushes builds one push to main after newMonorepoEnv and reports whether
// it was built, and the job's detail when it wasn't.
func pushes(t *testing.T, prepare func(env *ghEnv)) (built bool, detail string, env *ghEnv) {
	t.Helper()
	env = newMonorepoEnv(t)
	prepare(env)
	before, builds := env.aliases()["site.pier.test"], env.runner.count()
	env.push("p2", "main", commit("c"))
	j := env.run(context.Background())
	state, dep, detail := env.job(j.ID)
	if state == "skipped" {
		if dep != "" || env.runner.count() != builds || env.aliases()["site.pier.test"] != before {
			t.Fatalf("a skipped push built %q", dep)
		}
		if env.hub.lastStatus() != "success" || env.lastDetail() != skippedStatus {
			t.Fatalf("a skipped push was reported as %s %q", env.hub.lastStatus(), env.lastDetail())
		}
		return false, detail, env
	}
	if state != "done" || dep == "" || env.runner.count() == builds || env.aliases()["site.pier.test"] != dep {
		t.Fatalf("the push ended %s with %q", state, dep)
	}
	if env.hub.lastStatus() != "success" || env.lastDetail() != "Pier deployment ready." {
		t.Fatalf("a built push was reported as %s %q", env.hub.lastStatus(), env.lastDetail())
	}
	return true, "", env
}

func TestGitHubSkipsPushesThatLeaveTheRootAlone(t *testing.T) {
	b, c := commit("b"), commit("c")
	for _, tc := range []struct {
		name    string
		prepare func(env *ghEnv)
		built   bool
		detail  string
	}{
		{"only another app changed", func(env *ghEnv) {
			env.compare(b, c, "ahead", changed("apps/web/index.html", "README.md", ".github/workflows/ci.yml", "apps/docs-old/x")...)
		}, false, "Nothing in apps/docs, or what it uses, changed since bbbbbbb."},
		{"the root changed", func(env *ghEnv) {
			env.compare(b, c, "ahead", changed("apps/web/index.html", "apps/docs/index.html")...)
		}, true, ""},
		{"a file moved out of the root", func(env *ghEnv) {
			env.compare(b, c, "ahead", hubFile{Name: "apps/web/intro.html", Previous: "apps/docs/intro.html"})
		}, true, ""},
		{"a package docs uses changed", func(env *ghEnv) {
			env.compare(b, c, "ahead", changed("packages/ui/button.js")...)
		}, true, ""},
		{"the lockfile changed", func(env *ghEnv) {
			env.compare(b, c, "ahead", changed("apps/web/index.html", "pnpm-lock.yaml")...)
		}, true, ""},
		{"nothing changed", func(env *ghEnv) {
			env.compare(b, c, "identical")
		}, false, "Nothing changed since bbbbbbb."},
		{"always build", func(env *ghEnv) {
			env.settings(`{"root":"apps/docs","command":"make","output":".","always_build":true}`)
			env.compare(b, c, "ahead", changed("apps/web/index.html")...)
		}, true, ""},
		{"built from the whole repository", func(env *ghEnv) {
			env.settings(`{"root":"./","command":"make","output":"."}`)
			env.compare(b, c, "ahead", changed("apps/web/index.html")...)
		}, true, ""},
		{"built from another folder last", func(env *ghEnv) {
			env.settings(`{"root":"apps/web","command":"make","output":"."}`)
			env.compare(b, c, "ahead", changed("apps/docs/index.html")...)
		}, true, ""},
		{"a force push dropped commits", func(env *ghEnv) {
			env.compare(b, c, "diverged", changed("apps/web/index.html")...)
		}, true, ""},
		{"too many files to tell", func(env *ghEnv) {
			var files []hubFile
			for i := range maxCompareFiles {
				files = append(files, hubFile{Name: fmt.Sprintf("apps/web/%d.html", i)})
			}
			env.compare(b, c, "ahead", files...)
		}, true, ""},
		{"GitHub can't compare them", func(env *ghEnv) {}, true, ""},
		{"GitHub fails the comparison", func(env *ghEnv) {
			env.compare(b, c, "ahead", changed("apps/web/index.html")...)
			env.hub.mu.Lock()
			env.hub.fail = func(r *http.Request) int {
				if strings.Contains(r.URL.Path, "/compare/") {
					return 500
				}
				return 0
			}
			env.hub.mu.Unlock()
		}, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			built, detail, env := pushes(t, tc.prepare)
			if built != tc.built || detail != tc.detail {
				t.Fatalf("built %v with %q, want %v with %q", built, detail, tc.built, tc.detail)
			}
			if !built {
				events := env.call("GET", "/api/activity?project=site", "", ownerHeaders).Body.String()
				if !strings.Contains(events, `"title":"Skipped build"`) || !strings.Contains(events, tc.detail) {
					t.Fatalf("the skip isn't in the activity: %s", events)
				}
			}
		})
	}
}

func TestGitHubComparesWithTheLastBuildOfTheBranch(t *testing.T) {
	env := newMonorepoEnv(t)
	bg := context.Background()
	b := commit("b")

	// After a skipped push, the next is compared with the build before it.
	env.compare(b, commit("c"), "ahead", changed("apps/web/index.html")...)
	env.push("p2", "main", commit("c"))
	if j := env.run(bg); j.Deployment != "" {
		t.Fatalf("the push to apps/web built %s", j.Deployment)
	}
	env.compare(b, commit("d"), "ahead", changed("apps/web/index.html", "packages/ui/button.js")...)
	env.push("p3", "main", commit("d"))
	if j := env.run(bg); j.Deployment == "" {
		t.Fatal("the change to packages/ui since the last build was skipped")
	}

	// A branch with no build of its own is compared with production's head,
	// so its preview is only built once it changes the docs.
	env.compare(commit("d"), commit("e"), "ahead", changed("apps/web/index.html")...)
	env.push("p4", "feature", commit("e"))
	j := env.run(bg)
	if state, dep, detail := env.job(j.ID); state != "skipped" || dep != "" || detail != "Nothing in apps/docs, or what it uses, changed since ddddddd." {
		t.Fatalf("the branch ended %s %q %q", state, dep, detail)
	}
	env.compare(commit("d"), commit("f"), "diverged", changed("apps/docs/index.html")...)
	env.push("p5", "feature", commit("f"))
	if j := env.run(bg); j.Deployment == "" {
		t.Fatal("the branch's change to the docs was skipped")
	}
	// Then with its own build.
	env.compare(commit("f"), commit("1"), "ahead", changed("apps/web/next.config.mjs")...)
	env.push("p6", "feature", commit("1"))
	if j := env.run(bg); j.Deployment != "" {
		t.Fatalf("the branch's change to apps/web built %s", j.Deployment)
	}

	// A pull request at production's head is the same commit.
	env.pull("pr1", "opened", commit("d"), false)
	if j := env.run(bg); j.Deployment != "" {
		t.Fatalf("a pull request at production's head built %s", j.Deployment)
	} else if _, _, detail := env.job(j.ID); detail != "Nothing changed since ddddddd." {
		t.Fatalf("the pull request's detail is %q", detail)
	}
	env.idle()
}

func TestSettingsSaveAlwaysBuild(t *testing.T) {
	env := newGitHubEnv(t)
	if body := env.call("GET", "/api/projects/site/settings", "", ownerHeaders).Body.String(); !strings.Contains(body, `"always_build":false`) {
		t.Fatalf("settings: %s", body)
	}
	w := env.call("PUT", "/api/projects/site/settings", `{"build":{"root":"apps/docs","always_build":true}}`, ownerHeaders)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"always_build":true`) {
		t.Fatalf("saving answered %d %s", w.Code, w.Body)
	}
	if body := env.call("GET", "/api/projects/site/settings", "", ownerHeaders).Body.String(); !strings.Contains(body, `"always_build":true`) || !strings.Contains(body, `"root":"apps/docs"`) {
		t.Fatalf("settings after saving: %s", body)
	}
}
