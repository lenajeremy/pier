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

// githubRetry is how long the queue waits before each further try of a job
// that failed for a reason GitHub or the network should recover from.
var githubRetry = []time.Duration{2 * time.Second, 10 * time.Second, 30 * time.Second}

// githubStall is how long an archive download may go without a byte arriving.
var githubStall = time.Minute

// One consumer preserves delivery order. The row stays queued through the
// build and GitHub updates, so a daemon restart can finish reporting a build.
func (a *API) RunGitHub(ctx context.Context) {
	timer := time.NewTicker(2 * time.Second)
	defer timer.Stop()
	for ctx.Err() == nil {
		j, err := a.Store.NextGitHub(ctx)
		if err == nil {
			err = a.tryGitHub(ctx, &j)
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

// tryGitHub works a job, and works it again after a wait when GitHub or the
// network failed in a way that should pass. The row stays queued between
// tries, and each try reuses what the last one finished, so a 502 while
// reporting a build doesn't build it twice or leave the commit pending.
func (a *API) tryGitHub(ctx context.Context, j *store.GitHubJob) error {
	for try := 0; ; try++ {
		err := a.workGitHub(ctx, j)
		if err == nil || ctx.Err() != nil || try == len(githubRetry) || !temporary(err) {
			return err
		}
		a.Log.Info("GitHub delivery will be tried again", "project", j.Project, "wait", githubRetry[try], "error", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(githubRetry[try]):
		}
	}
}

// previewBranch is the branch a job's preview is filed and addressed under.
// pr-N is kept for pull requests, including after slugging, so a branch
// whose name would take one is given a prefix.
func previewBranch(j store.GitHubJob) string {
	if j.PR == 0 && strings.HasPrefix(names.BranchSlug(j.Branch, j.Project), "pr-") {
		return "branch-" + j.Branch
	}
	return j.Branch
}

// retirePreview stops the address of a closed pull request or a deleted
// branch from serving. It waits its turn in the queue, so a build of the
// same preview that was already running can't bring the address back.
func (a *API) retirePreview(ctx context.Context, j *store.GitHubJob) error {
	host := names.Branch(j.Project, previewBranch(*j), a.Deployer.Domain)
	removed, err := a.Store.RemoveBranchAlias(ctx, j.Project, host)
	if err != nil {
		return err
	}
	if removed {
		if err := a.Deployer.Sync(ctx); err != nil {
			a.Log.Warn("router behind after retiring a preview; it will retry", "host", host, "err", err)
		}
		a.event(ctx, j.Project, "Retired preview", host, "")
	}
	return a.Store.SaveGitHubJob(ctx, j.ID, "done", "", "")
}

func (a *API) workGitHub(ctx context.Context, j *store.GitHubJob) error {
	if j.SHA == "" {
		return a.retirePreview(ctx, j)
	}
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
	// On a retry, reuse the recorded result. A build that pierd stopped
	// during has none: it stays visible as failed, and the commit builds
	// again, unless pierd has stopped during it too often.
	var dep store.Deployment
	if j.Deployment != "" {
		dep, err = a.Store.Deployment(ctx, j.Deployment)
		if err != nil {
			return err
		}
	}
	if j.Detail == store.InterruptedBuild || j.Deployment != "" && dep.Interrupted() {
		gaveUp, err := a.Store.InterruptGitHubBuild(ctx, j.ID, j.Deployment)
		if err != nil {
			return err
		}
		if gaveUp {
			a.Log.Warn("GitHub build keeps being cut short by pierd stopping; it waits for the owner", "project", j.Project, "job", j.ID)
			return nil
		}
		j.Deployment, j.Detail = "", ""
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
		if target == store.TargetPreview {
			src.Branch = previewBranch(*j)
		}
		// Note the stop on the job the moment it comes. pierd can exit before
		// the build has unwound, and by then its record may already say failed.
		job := j.ID
		noted := make(chan struct{})
		unwatch := context.AfterFunc(ctx, func() {
			defer close(noted)
			if err := a.Store.NoteGitHubJob(context.WithoutCancel(ctx), job, store.InterruptedBuild); err != nil {
				a.Log.Warn("note interrupted GitHub build", "job", job, "err", err)
			}
		})
		buildErr := a.Deployer.DeployGitHub(ctx, j.Project, archive, src, io.Discard)
		// A note still being written would land after the detach below, and
		// the next start would count this stop a second time.
		if !unwatch() {
			<-noted
		}
		if ctx.Err() != nil {
			// pierd is stopping, and a build it cut short was recorded as
			// failed. Detach it so the next start builds the commit again
			// instead of reporting a failure the commit didn't cause.
			if buildErr != nil && j.Deployment != "" {
				gaveUp, err := a.Store.InterruptGitHubBuild(context.WithoutCancel(ctx), j.ID, j.Deployment)
				if err != nil {
					a.Log.Warn("detach interrupted GitHub build", "project", j.Project, "err", err)
				}
				if gaveUp {
					a.Log.Warn("GitHub build keeps being cut short by pierd stopping; it waits for the owner", "project", j.Project, "job", j.ID)
				}
			}
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

// previewMarker finds Pier's comment on a pull request. There is one per
// pull request, so each push rewrites it instead of adding another.
const previewMarker = "<!-- pier-preview -->"

func (a *API) ghPreviewComment(ctx context.Context, token, repo string, appID int64, j store.GitHubJob, link string) error {
	path := fmt.Sprintf("/repos/%s/issues/%d/comments", repo, j.PR)
	msg := map[string]string{"body": fmt.Sprintf("Pier preview for `%s`: %s\n\n%s", j.SHA[:7], link, previewMarker)}
	for page := 1; ; page++ {
		var comments []struct {
			ID   int64
			Body string
			App  struct{ ID int64 } `json:"performed_via_github_app"`
		}
		if err := a.ghJSON(ctx, "GET", fmt.Sprintf("%s?per_page=100&page=%d", path, page), token, nil, &comments); err != nil {
			return err
		}
		for _, c := range comments {
			if c.App.ID != appID || !strings.Contains(c.Body, previewMarker) {
				continue
			}
			if c.Body == msg["body"] {
				return nil
			}
			return a.ghJSON(ctx, "PATCH", fmt.Sprintf("/repos/%s/issues/comments/%d", repo, c.ID), token, msg, nil)
		}
		if len(comments) < 100 {
			break
		}
	}
	return a.ghJSON(ctx, "POST", path, token, msg, nil)
}

// progress pushes a download's stall deadline back each time bytes arrive.
type progress struct {
	r     io.Reader
	stall *time.Timer
}

func (p progress) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.stall.Reset(githubStall)
	}
	return n, err
}

// downloadGitHub saves a commit's source archive. It has no overall time
// limit, because a large repository on a slow link takes as long as it
// takes. It ends when pierd stops or nothing has arrived for githubStall.
func (a *API) downloadGitHub(ctx context.Context, token, repo, sha string) (string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stall := time.AfterFunc(githubStall, cancel)
	defer stall.Stop()
	r, err := a.ghSend(ctx, githubArchive, "GET", "/repos/"+repo+"/tarball/"+sha, token, nil)
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
	n, err := io.Copy(f, io.LimitReader(progress{r.Body, stall}, MaxUpload+1))
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		// Whatever cut the download short, another try may get all of it.
		err = &ghError{msg: "downloading the repository archive: " + err.Error()}
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
