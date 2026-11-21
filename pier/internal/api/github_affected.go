package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"

	"pier/internal/build"
	"pier/internal/monorepo"
	"pier/internal/store"
)

// maxCompareFiles is as many changed files as GitHub's comparison lists. One
// that lists this many may have left some out.
const maxCompareFiles = 300

// unaffected reports whether a push leaves alone everything a project builds
// from, so building it would make what the project's last build made. Only
// a project built from a folder of its repository can be left alone, and
// reason, for the job's detail, says what was compared. archive is the
// commit's source archive, read for the workspace when a changed file is
// outside the folder.
//
// The push is compared with the commit this project last built on the same
// branch. A branch or pull request that has none is compared with the
// production branch's head, so a preview is built when it changes this
// project; production with none is always built.
func (a *API) unaffected(ctx context.Context, j *store.GitHubJob, token, repo string, project store.Project, archive string) (skip bool, reason string, err error) {
	root, err := build.Folder(project.Build.Root)
	if err != nil || root == "." || project.Build.AlwaysBuild || j.SHA == "" {
		return false, "", nil
	}
	if imported, err := a.Store.ImportJob(ctx, j.ID); err != nil || imported {
		return false, "", err
	}
	production := project.ProductionBranch
	if production == "" {
		production = a.Deployer.ProductionBranch
	}
	branch := previewBranch(*j)
	if j.PR == 0 && j.Branch == production {
		branch = j.Branch
	}
	base, own, err := a.lastBuilt(ctx, project.Name, branch, root)
	if err != nil || base == "" && own {
		return false, "", err
	}
	if base == "" {
		if branch == production && j.PR == 0 {
			return false, "", nil
		}
		if base, err = a.ghHead(ctx, token, repo, production, 0); err != nil || !commitSHA.MatchString(base) {
			return false, "", err
		}
	}
	since := base[:7]
	if base == j.SHA {
		return true, "Nothing changed since " + since + ".", nil
	}

	var compare struct {
		Status string `json:"status"`
		Files  []struct {
			Name     string `json:"filename"`
			Previous string `json:"previous_filename"`
		} `json:"files"`
	}
	// Changed files are listed in full on the first page, whatever its size,
	// so one commit a page keeps the answer small.
	err = a.ghJSON(ctx, "GET", "/repos/"+repo+"/compare/"+base+"..."+j.SHA+"?per_page=1", token, nil, &compare)
	if e := (*ghError)(nil); errors.As(err, &e) && e.Status == http.StatusNotFound {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	if compare.Status == "identical" {
		return true, "Nothing changed since " + since + ".", nil
	}
	// A comparison lists what the push changed since the commit both share.
	// When the last build had commits the push doesn't, as after a force
	// push, they differ in ways the list doesn't show. A preview compared
	// with production's head only asks what the branch changed, which this
	// answers.
	if own && compare.Status != "ahead" || len(compare.Files) >= maxCompareFiles {
		return false, "", nil
	}
	var changed []string
	for _, f := range compare.Files {
		changed = append(changed, f.Name)
		if f.Previous != "" {
			changed = append(changed, f.Previous)
		}
	}
	if monorepo.Affected(root, nil, changed) {
		return false, "", nil
	}
	f, err := os.Open(archive)
	if err != nil {
		return false, "", err
	}
	defer f.Close()
	source, err := monorepo.ReadArchive(f, true)
	if err != nil {
		return false, "", fmt.Errorf("reading the workspace: %w", err)
	}
	uses := source.Workspace().Uses(root)
	if monorepo.Affected(root, uses, changed) {
		return false, "", nil
	}
	if len(uses) == 0 {
		return true, fmt.Sprintf("Nothing in %s changed since %s.", root, since), nil
	}
	return true, fmt.Sprintf("Nothing in %s, or what it uses, changed since %s.", root, since), nil
}

// lastBuilt finds the commit of the project's newest build on branch: one
// that built or failed by itself, not one pierd stopping ended. own is true
// when the branch has such a build. A build that can't stand for the commit
// gives no commit, with own true: one not from GitHub, or from another
// folder than root, since settings changed after it.
func (a *API) lastBuilt(ctx context.Context, project, branch, root string) (commit string, own bool, err error) {
	deployments, err := a.Store.Deployments(ctx, project)
	if err != nil {
		return "", false, err
	}
	for _, d := range deployments {
		if d.Branch != branch || d.Status != store.Ready && d.Status != store.Failed || d.Interrupted() {
			continue
		}
		var plan struct{ Root string }
		if d.Source != store.SourceGitHub || !commitSHA.MatchString(d.Commit) || json.Unmarshal([]byte(d.Plan), &plan) != nil || plan.Root != root {
			return "", true, nil
		}
		return d.Commit, true, nil
	}
	return "", false, nil
}
