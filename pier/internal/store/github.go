package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// GitHubJob records an immutable commit before acknowledging its delivery.
// Review and retries always refer to this SHA, including after a restart.
// A job with no SHA retires the preview of a closed pull request or a
// deleted branch instead of building one.
//
// A job is queued until its result is reported, and a fork's pull request
// waits in approval first. It ends as done, superseded or dismissed; an
// error waits for the owner to try it again or dismiss it.
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
	_, err = tx.ExecContext(ctx, `INSERT INTO projects(name,created_at,production_branch,repo,root_dir,preset,install_cmd,build_cmd,output_dir) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(name) DO NOTHING`, p.Name, time.Now().UnixMilli(), p.ProductionBranch, p.Repo, p.Build.Root, p.Build.Preset, p.Build.Install, p.Build.Command, p.Build.Output)
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
	_, err = tx.ExecContext(ctx, `INSERT INTO github_jobs(project,delivery,branch,sha,pr,state) VALUES(?,?,?,?,0,'queued')`, p.Name, "import", p.ProductionBranch, sha)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// QueueGitHub records a signed delivery for the project that uses the
// repository and returns that project, or "" when no project does. The
// delivery names the installation that can read the repository today. That
// changes when the App is reinstalled or the repository moves, so it replaces
// the one recorded at import. An empty sha retires the preview.
func (s *Store) QueueGitHub(ctx context.Context, repoID, installation int64, delivery, branch, sha string, pr int, review bool) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var project string
	err = tx.QueryRowContext(ctx, "SELECT project FROM github_repos WHERE repo_id=?", repoID).Scan(&project)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE github_repos SET installation=? WHERE repo_id=?", installation, repoID); err != nil {
		return "", err
	}
	state := "queued"
	if review {
		state = "approval"
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO github_jobs(project,delivery,branch,sha,pr,state) VALUES(?,?,?,?,?,?)
 ON CONFLICT(project,delivery) DO NOTHING`, project, delivery, branch, sha, pr, state)
	if err != nil {
		return "", err
	}
	// Anyone can open a pull request from a fork and keep pushing to it. Only
	// its newest commit is worth the owner's review, and none once it closes.
	if n, _ := res.RowsAffected(); n == 1 && pr > 0 {
		id, _ := res.LastInsertId()
		detail := "A newer commit was pushed to this pull request."
		if sha == "" {
			detail = "The pull request was closed."
		}
		if _, err = tx.ExecContext(ctx, "UPDATE github_jobs SET state='superseded',detail=? WHERE project=? AND pr=? AND state='approval' AND id<?", detail, project, pr, id); err != nil {
			return "", err
		}
	}
	return project, tx.Commit()
}

const githubJobCols = `SELECT j.id,j.project,r.repo_id,r.installation,j.branch,j.sha,j.pr,j.state,j.deployment,j.detail FROM github_jobs j JOIN github_repos r ON j.project=r.project `

func scanJob(r interface{ Scan(...any) error }) (GitHubJob, error) {
	var j GitHubJob
	err := r.Scan(&j.ID, &j.Project, &j.RepoID, &j.Installation, &j.Branch, &j.SHA, &j.PR, &j.State, &j.Deployment, &j.Detail)
	return j, err
}

func (s *Store) NextGitHub(ctx context.Context) (GitHubJob, error) {
	return scanJob(s.db.QueryRowContext(ctx, githubJobCols+"WHERE j.state='queued' ORDER BY j.id LIMIT 1"))
}
func (s *Store) GitHubAttention(ctx context.Context) ([]GitHubJob, error) {
	rows, err := s.db.QueryContext(ctx, githubJobCols+"WHERE j.state IN ('approval','error') ORDER BY j.id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []GitHubJob{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// SaveGitHubJob records how the queue left a job. It only moves a job that is
// still queued, so one that was cancelled during its build stays cancelled.
func (s *Store) SaveGitHubJob(ctx context.Context, id int64, state, dep, detail string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE github_jobs SET state=?,deployment=?,detail=? WHERE id=? AND state='queued'", state, dep, detail, id)
	return err
}

// ResumeGitHub queues a job that waits for the owner: an approved pull
// request from a fork, or an error to try again.
func (s *Store) ResumeGitHub(ctx context.Context, id int64) error {
	return s.answerGitHub(ctx, id, "queued", "")
}

// DismissGitHub ends a job that waits for the owner without building it.
func (s *Store) DismissGitHub(ctx context.Context, id int64) error {
	return s.answerGitHub(ctx, id, "dismissed", "Dismissed by the owner.")
}

func (s *Store) answerGitHub(ctx context.Context, id int64, state, detail string) error {
	r, err := s.db.ExecContext(ctx, "UPDATE github_jobs SET state=?,detail=? WHERE id=? AND state IN ('approval','error')", state, detail, id)
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
// The job stays queued, so the commit is built again. deployment may be
// empty when the stop came before the build had a record, and a build that
// did finish keeps its own result.
func (s *Store) InterruptGitHubBuild(ctx context.Context, job int64, deployment string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE deployments SET status=?,error=? WHERE id=? AND status IN (?,?)", Failed, InterruptedBuild, deployment, Building, Failed); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE github_jobs SET deployment='',detail='' WHERE id=? AND state='queued'", job); err != nil {
		return err
	}
	return tx.Commit()
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

func (s *Store) GitHubProject(ctx context.Context, id int64) (string, error) {
	var name string
	err := s.db.QueryRowContext(ctx, "SELECT project FROM github_repos WHERE repo_id=?", id).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return name, err
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
