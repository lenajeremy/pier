package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"time"
)

// GitHubJob records an immutable commit before acknowledging its delivery.
// Review and retries always refer to this SHA, including after a restart.
// A job with no SHA retires the preview of a closed pull request or a
// deleted branch instead of building one.
//
// A job is queued until its result is reported, and a fork's pull request
// waits in approval first. It ends as done, skipped (the push changed
// nothing its project builds from, so nothing was built), superseded or
// dismissed; an error waits for the owner to try it again or dismiss it.
type GitHubJob struct {
	ID           int64  `json:"id"`
	Project      string `json:"project"`
	RepoID       int64  `json:"repo_id"`
	Installation int64  `json:"installation"`
	Branch       string `json:"branch"`
	SHA          string `json:"sha"`
	PR           int    `json:"pr"`
	State        string `json:"state"`
	Deployment   string `json:"deployment"`
	Detail       string `json:"detail"`
}

// InterruptedBuild is the error on a deployment that pierd stopped during,
// and the note on its job until the commit is built again.
const InterruptedBuild = "pierd stopped during this build"

// A build that is itself what stops pierd, by running it out of memory say,
// would start again on every start. After maxInterruptions stops the job
// waits for the owner instead, with UnsteadyBuild as its detail.
const (
	maxInterruptions = 3
	UnsteadyBuild    = "pierd stopped during this build 3 times, so Pier stopped trying. Try again once the server is steady."
)

// Interrupted reports whether pierd stopping ended this deployment, rather
// than its own build failing. A record still building has no process behind
// it once the queue reads it, and InterruptAppBuilds has its own wording.
func (d Deployment) Interrupted() bool {
	return d.Status == Building || d.Status == Failed && (d.Error == InterruptedBuild || strings.HasPrefix(d.Error, "Pier restarted"))
}

func (s *Store) ImportGitHub(ctx context.Context, p Project, repoID, installation int64, sha string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO projects(name,created_at,production_branch,repo,root_dir,preset,install_cmd,build_cmd,output_dir,always_build) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(name) DO NOTHING`, p.Name, time.Now().UnixMilli(), p.ProductionBranch, p.Repo, p.Build.Root, p.Build.Preset, p.Build.Install, p.Build.Command, p.Build.Output, p.Build.AlwaysBuild)
	if err != nil {
		return err
	}
	var changed int
	if err = tx.QueryRowContext(ctx, "SELECT changes()").Scan(&changed); err != nil {
		return err
	}
	if changed == 0 {
		return ErrExists
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO github_repos(project,repo_id,installation) VALUES(?,?,?)", p.Name, repoID, installation)
	if err != nil {
		return err
	}
	// The installation was just checked against the repository, so the
	// repository's other projects follow it too.
	if _, err = tx.ExecContext(ctx, "UPDATE github_repos SET installation=? WHERE repo_id=?", installation, repoID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO github_jobs(project,delivery,branch,sha,pr,state) VALUES(?,?,?,?,0,'queued')`, p.Name, importDelivery, p.ProductionBranch, sha)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// importDelivery files the first build of an imported project. Every import
// shares it, so unlike a delivery from GitHub it never ties one project's
// job to another's.
const importDelivery = "import"

// querier is a database or a transaction to read from.
type querier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// GitHubProjects lists the projects that deploy a repository, by name. A
// repository can hold several, each built from its own folder.
func (s *Store) GitHubProjects(ctx context.Context, repoID int64) ([]string, error) {
	return repoProjects(ctx, s.db, repoID)
}

func repoProjects(ctx context.Context, q querier, repoID int64) ([]string, error) {
	rows, err := q.QueryContext(ctx, "SELECT project FROM github_repos WHERE repo_id=? ORDER BY project", repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	projects := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		projects = append(projects, name)
	}
	return projects, rows.Err()
}

// QueueGitHub records a signed delivery once for each project that uses the
// repository and returns those projects by name, or none when no project
// does. Each project builds and reports on its own, so each gets its own job.
// The delivery names the installation that can read the repository today.
// That changes when the App is reinstalled or the repository moves, so it
// replaces the one recorded at import. An empty sha retires the preview.
// updated is when GitHub last updated the pull request, as the delivery
// says, and is zero for a push.
func (s *Store) QueueGitHub(ctx context.Context, repoID, installation int64, delivery, branch, sha string, pr int, review bool, updated time.Time) ([]string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	projects, err := repoProjects(ctx, tx, repoID)
	if err != nil || len(projects) == 0 {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE github_repos SET installation=? WHERE repo_id=?", installation, repoID); err != nil {
		return nil, err
	}
	var at int64
	if !updated.IsZero() {
		at = updated.UnixMilli()
	}
	for _, project := range projects {
		if err = queueJob(ctx, tx, project, delivery, branch, sha, pr, review, at); err != nil {
			return nil, err
		}
	}
	return projects, tx.Commit()
}

// queueJob files one project's job for a delivery. at is GitHub's time for
// a pull request, in Unix milliseconds, or 0.
func queueJob(ctx context.Context, tx *sql.Tx, project, delivery, branch, sha string, pr int, review bool, at int64) error {
	state := "queued"
	if review {
		state = "approval"
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO github_jobs(project,delivery,branch,sha,pr,state,pr_updated) VALUES(?,?,?,?,?,?,?)
 ON CONFLICT(project,delivery) DO NOTHING`, project, delivery, branch, sha, pr, state, at)
	if err != nil {
		return err
	}
	// Anyone can open a pull request from a fork and keep pushing to it. Only
	// its newest commit is worth the owner's review, and none once it closes.
	// Newest is by GitHub's time, not arrival, since GitHub can deliver an
	// older push late; deliveries in the same second go by arrival. A late
	// one is kept, already superseded, so its delivery reads as handled.
	if n, _ := res.RowsAffected(); n == 1 && pr > 0 {
		if sha == "" {
			_, err = tx.ExecContext(ctx, "UPDATE github_jobs SET state='superseded',detail='The pull request was closed.' WHERE project=? AND pr=? AND state='approval'", project, pr)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE github_jobs SET state='superseded',detail='A newer commit was pushed to this pull request.'
 WHERE project=? AND pr=? AND state='approval' AND id<>(SELECT id FROM github_jobs WHERE project=? AND pr=? AND state='approval' ORDER BY pr_updated DESC,id DESC LIMIT 1)`, project, pr, project, pr)
		}
		if err != nil {
			return err
		}
		// A late one can also come after GitHub's newer news was approved,
		// built or closed, when no approval is left to compare it with.
		if review && at > 0 {
			id, _ := res.LastInsertId()
			_, err = tx.ExecContext(ctx, `UPDATE github_jobs SET state='superseded',detail=(SELECT CASE WHEN o.sha='' THEN 'The pull request was closed.' ELSE 'A newer commit was pushed to this pull request.' END
 FROM github_jobs o WHERE o.project=? AND o.pr=? AND o.pr_updated>? ORDER BY o.pr_updated DESC,o.id DESC LIMIT 1)
 WHERE id=? AND state='approval' AND EXISTS(SELECT 1 FROM github_jobs o WHERE o.project=? AND o.pr=? AND o.pr_updated>?)`, project, pr, at, id, project, pr, at)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

const githubJobCols = `SELECT j.id,j.project,r.repo_id,r.installation,j.branch,j.sha,j.pr,j.state,j.deployment,j.detail FROM github_jobs j JOIN github_repos r ON j.project=r.project `

// scanJob reads a job's columns, then any more the query asked for into more.
func scanJob(r interface{ Scan(...any) error }, more ...any) (GitHubJob, error) {
	var j GitHubJob
	err := r.Scan(append([]any{&j.ID, &j.Project, &j.RepoID, &j.Installation, &j.Branch, &j.SHA, &j.PR, &j.State, &j.Deployment, &j.Detail}, more...)...)
	return j, err
}

func (s *Store) NextGitHub(ctx context.Context) (GitHubJob, error) {
	return scanJob(s.db.QueryRowContext(ctx, githubJobCols+"WHERE j.state='queued' ORDER BY j.id LIMIT 1"))
}

// GitHubWaiting is a delivery that waits for the owner. A delivery for a
// repository with several projects makes a job for each, but the owner
// answers the commit once, so they wait as one: the delivery's first job
// that waits this way, and Projects, every project whose job does.
type GitHubWaiting struct {
	GitHubJob
	Projects []string `json:"projects"`
}

// GitHubAttention lists the deliveries that wait for the owner, newest first.
func (s *Store) GitHubAttention(ctx context.Context) ([]GitHubWaiting, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT j.id,j.project,r.repo_id,r.installation,j.branch,j.sha,j.pr,j.state,j.deployment,j.detail,
 CASE WHEN j.delivery=? THEN j.delivery||'/'||j.project ELSE j.delivery END
 FROM github_jobs j JOIN github_repos r ON j.project=r.project WHERE j.state IN ('approval','error') ORDER BY j.id`, importDelivery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	waiting := []GitHubWaiting{}
	group := map[[2]string]int{} // a delivery and a state, to its place in waiting
	for rows.Next() {
		var delivery string
		j, err := scanJob(rows, &delivery)
		if err != nil {
			return nil, err
		}
		key := [2]string{delivery, j.State}
		if i, ok := group[key]; ok {
			waiting[i].Projects = append(waiting[i].Projects, j.Project)
			continue
		}
		group[key] = len(waiting)
		waiting = append(waiting, GitHubWaiting{GitHubJob: j, Projects: []string{j.Project}})
	}
	for i := range waiting {
		slices.Sort(waiting[i].Projects)
	}
	slices.Reverse(waiting)
	return waiting, rows.Err()
}

// SaveGitHubJob records how the queue left a job. It only moves a job that is
// still queued, so one that was cancelled during its build stays cancelled.
func (s *Store) SaveGitHubJob(ctx context.Context, id int64, state, dep, detail string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE github_jobs SET state=?,deployment=?,detail=? WHERE id=? AND state='queued'", state, dep, detail, id)
	return err
}

// ResumeGitHub queues a job that waits for the owner: an approved pull
// request from a fork, or an error to try again. Trying again starts the
// count of times pierd stopped during its build over.
func (s *Store) ResumeGitHub(ctx context.Context, id int64) error {
	return s.answerGitHub(ctx, id, "queued", "")
}

// DismissGitHub ends a job that waits for the owner without building it.
func (s *Store) DismissGitHub(ctx context.Context, id int64) error {
	return s.answerGitHub(ctx, id, "dismissed", "Dismissed by the owner.")
}

// answerGitHub answers a job, and with it every job its delivery made for
// the repository's other projects that waits the same way, as
// GitHubAttention shows them as one.
func (s *Store) answerGitHub(ctx context.Context, id int64, state, detail string) error {
	r, err := s.db.ExecContext(ctx, `UPDATE github_jobs SET state=?,detail=?,interrupted=0 WHERE id IN
 (SELECT o.id FROM github_jobs j JOIN github_jobs o ON o.state=j.state AND o.delivery=j.delivery AND (o.id=j.id OR j.delivery<>?)
 WHERE j.id=? AND j.state IN ('approval','error'))`, state, detail, importDelivery, id)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return err
}

// NoteGitHubJob leaves a note on a queued job for the next time it runs.
func (s *Store) NoteGitHubJob(ctx context.Context, id int64, note string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE github_jobs SET detail=? WHERE id=? AND state='queued'", note, id)
	return err
}

// InterruptGitHubBuild records that pierd stopping, not the commit, ended a
// job's build, and detaches the build and any note of the stop from the job.
// The job stays queued, so the commit is built again, until this is the
// job's maxInterruptions-th stop: then it ends as an error for the owner to
// try again, and gaveUp says so. deployment may be empty when the stop came
// before the build had a record, and a build that did finish keeps its own
// result.
func (s *Store) InterruptGitHubBuild(ctx context.Context, job int64, deployment string) (gaveUp bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE deployments SET status=?,error=? WHERE id=? AND status IN (?,?)", Failed, InterruptedBuild, deployment, Building, Failed); err != nil {
		return false, err
	}
	// A job that isn't queued any more was answered during its build, and
	// keeps that answer.
	var stops int
	err = tx.QueryRowContext(ctx, "UPDATE github_jobs SET deployment='',detail='',interrupted=interrupted+1 WHERE id=? AND state='queued' RETURNING interrupted", job).Scan(&stops)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if gaveUp = stops >= maxInterruptions; gaveUp {
		if _, err = tx.ExecContext(ctx, "UPDATE github_jobs SET state='error',detail=? WHERE id=?", UnsteadyBuild, job); err != nil {
			return false, err
		}
	}
	return gaveUp, tx.Commit()
}

// DisconnectGitHub forgets the App and any registration in progress, which
// are the settings named by keys, and returns how many jobs it dismissed.
// Projects keep their repositories. Unfinished builds are dismissed because
// they belong to installations of the old App, which a new App can't use:
// the first delivery from the new App records its installation and builds
// the branch's latest commit. Retiring a preview needs no App, so those
// jobs stay.
func (s *Store) DisconnectGitHub(ctx context.Context, keys ...string) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	for _, key := range keys {
		if _, err = tx.ExecContext(ctx, "DELETE FROM settings WHERE key=?", key); err != nil {
			return 0, err
		}
	}
	res, err := tx.ExecContext(ctx, "UPDATE github_jobs SET state='dismissed',detail='GitHub was disconnected before this ran.' WHERE state IN ('queued','approval','error') AND sha<>''")
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, tx.Commit()
}

// RemoveBranchAlias stops a branch or pull request address from serving and
// reports whether it was. Without an address, the deployment it pointed at
// becomes one that preview cleanup may remove. Production's address stays.
func (s *Store) RemoveBranchAlias(ctx context.Context, project, host string) (bool, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM aliases WHERE host=? AND project=? AND kind=?", host, project, KindBranch)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// BranchPreview returns the deployment a branch's preview address serves,
// and true. A branch whose address isn't serving has its newest ready
// deployment from GitHub instead, and false. ErrNotFound means it has
// neither.
func (s *Store) BranchPreview(ctx context.Context, project, branch, host string) (Deployment, bool, error) {
	d, err := scanDeployment(s.db.QueryRowContext(ctx, `SELECT `+deploymentCols+` FROM deployments
 WHERE id=(SELECT deployment_id FROM aliases WHERE host=? AND project=? AND kind=?)`, host, project, KindBranch))
	if !errors.Is(err, sql.ErrNoRows) {
		return d, err == nil, err
	}
	d, err = scanDeployment(s.db.QueryRowContext(ctx, `SELECT `+deploymentCols+` FROM deployments
 WHERE project=? AND branch=? AND status=? AND source=? ORDER BY created_at DESC, id LIMIT 1`, project, branch, Ready, SourceGitHub))
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return d, false, err
}

// StartGitHubDeployment binds the job and the building record atomically.
func (s *Store) StartGitHubDeployment(ctx context.Context, job int64, d Deployment) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertDeployment(ctx, tx, d); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, "UPDATE github_jobs SET deployment=? WHERE id=? AND project=? AND state='queued'", d.ID, job, d.Project)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrNotFound
	}
	return tx.Commit()
}
