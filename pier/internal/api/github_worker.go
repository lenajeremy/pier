package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"pier/internal/deploy"
	"pier/internal/names"
	"pier/internal/store"
)

// One consumer preserves delivery order. The row stays queued through the
// build and GitHub updates, so a daemon restart can finish reporting a build.
func (a *API) RunGitHub(ctx context.Context) {
	timer := time.NewTicker(2 * time.Second)
	defer timer.Stop()
	for ctx.Err() == nil {
		j, err := a.Store.NextGitHub(ctx)
		if err == nil {
			err = a.workGitHub(ctx, &j)
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				a.Log.Warn("GitHub delivery", "project", j.Project, "error", err)
				err = a.Store.SaveGitHubJob(ctx, j.ID, "error", j.Deployment, err.Error())
			}
			if err == nil {
				continue
			}
		}
		if !errors.Is(err, sql.ErrNoRows) {
			a.Log.Warn("GitHub queue", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}

func (a *API) workGitHub(ctx context.Context, j *store.GitHubJob) error {
	app, err := a.loadApp(ctx)
	if err != nil {
		return err
	}
	token, err := a.ghToken(ctx, app, j.Installation, j.RepoID)
	if err != nil {
		return err
	}
	repo, err := a.ghRepo(ctx, token, j.RepoID)
	if err != nil {
		return err
	}
	project, err := a.Store.Project(ctx, j.Project)
	if err != nil {
		return err
	}
	statusPath := "/repos/" + repo.Name + "/statuses/" + j.SHA
	status := func(state, description, link string) error {
		msg := map[string]string{"state": state, "description": description, "context": "Pier / " + j.Project + " / " + j.Branch}
		if len(msg["context"]) > 100 {
			msg["context"] = "Pier / " + j.Project
		}
		if link != "" {
			msg["target_url"] = link
		}
		return a.ghJSON(ctx, "POST", statusPath, token, msg, nil)
	}
	// On a retry, reuse the recorded result. A daemon interruption during a
	// build leaves its old deployment visible as failed and starts a fresh one.
	var dep store.Deployment
	if j.Deployment != "" {
		dep, err = a.Store.Deployment(ctx, j.Deployment)
		if err != nil {
			return err
		}
		if dep.Status == store.Building {
			dep.Status = store.Failed
			dep.Error = "pierd stopped during this build"
			if err = a.Store.FinishDeployment(ctx, dep); err != nil {
				return err
			}
			j.Deployment = ""
		}
	}
	if j.Deployment == "" {
		head, err := a.ghHead(ctx, token, repo.Name, j.Branch, j.PR)
		if err != nil {
			return err
		}
		if head != j.SHA {
			return a.Store.SaveGitHubJob(ctx, j.ID, "superseded", "", "The branch or pull request has moved.")
		}
		if err = status("pending", "Pier is building this commit.", ""); err != nil {
			return err
		}
		archive, err := a.downloadGitHub(ctx, token, repo.Name, j.SHA)
		if err != nil {
			return err
		}
		defer os.Remove(archive)
		target := store.TargetPreview
		production := project.ProductionBranch
		if production == "" {
			production = a.Deployer.ProductionBranch
		}
		if j.PR == 0 && j.Branch == production {
			target = store.TargetProduction
		}
		var commit struct{ Message string }
		if err := a.ghJSON(ctx, "GET", "/repos/"+repo.Name+"/git/commits/"+j.SHA, token, nil, &commit); err != nil {
			return err
		}
		subject, _, _ := strings.Cut(strings.TrimSpace(commit.Message), "\n")
		src := deploy.Source{
			GitHubJob: j.ID, Branch: j.Branch, Commit: j.SHA, Target: target,
			Message: subject, OnStart: func(id string) { j.Deployment = id },
		}
		// Reserve pr-N preview names for pull requests, including after slugging.
		if j.PR == 0 && target == store.TargetPreview && strings.HasPrefix(names.BranchSlug(j.Branch, j.Project), "pr-") {
			src.Branch = "branch-" + j.Branch
		}
		buildErr := a.Deployer.DeployGitHub(ctx, j.Project, archive, src, io.Discard)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if j.Deployment == "" {
			return buildErr
		}
		dep, err = a.Store.Deployment(ctx, j.Deployment)
		if err != nil {
			return err
		}
		if buildErr != nil && dep.Status != store.Failed {
			return buildErr
		}
	}
	result := "success"
	description := "Pier deployment ready."
	link := a.Deployer.URL(names.Deployment(j.Project, dep.ID, a.Deployer.Domain))
	if dep.Status != store.Ready {
		result = "failure"
		description = "Pier build failed."
		link = a.Deployer.URL(a.Deployer.PierHost) + "/projects/" + j.Project + "/deployments/" + dep.ID
	}
	if err = status(result, description, link); err != nil {
		return err
	}
	if j.PR > 0 && dep.Status == store.Ready {
		if err := a.ghPreviewComment(ctx, token, repo.Name, app.ID, *j, link); err != nil {
			return err
		}
	}
	return a.Store.SaveGitHubJob(ctx, j.ID, "done", dep.ID, "")
}

// A deployment-specific marker avoids duplicate comments after retries.
func (a *API) ghPreviewComment(ctx context.Context, token, repo string, appID int64, j store.GitHubJob, link string) error {
	path := fmt.Sprintf("/repos/%s/issues/%d/comments", repo, j.PR)
	marker := "<!-- pier-deployment-" + j.Deployment + " -->"
	for page := 1; ; page++ {
		var comments []struct {
			Body string
			App  struct{ ID int64 } `json:"performed_via_github_app"`
		}
		if err := a.ghJSON(ctx, "GET", fmt.Sprintf("%s?per_page=100&page=%d", path, page), token, nil, &comments); err != nil {
			return err
		}
		for _, c := range comments {
			if c.App.ID == appID && strings.Contains(c.Body, marker) {
				return nil
			}
		}
		if len(comments) < 100 {
			break
		}
	}
	return a.ghJSON(ctx, "POST", path, token, map[string]string{"body": fmt.Sprintf("Pier preview for `%s`: %s\n\n%s", j.SHA[:7], link, marker)}, nil)
}

func (a *API) downloadGitHub(ctx context.Context, token, repo, sha string) (string, error) {
	r, err := a.ghRequest(ctx, "GET", "/repos/"+repo+"/tarball/"+sha, token, nil)
	if err != nil {
		return "", err
	}
	defer r.Body.Close()
	if err = os.MkdirAll(a.Deployer.Tmp, 0700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(a.Deployer.Tmp, "github-*.tar.gz")
	if err != nil {
		return "", err
	}
	n, err := io.Copy(f, io.LimitReader(r.Body, MaxUpload+1))
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if n > MaxUpload {
		err = fmt.Errorf("the repository archive exceeds the upload limit")
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}
