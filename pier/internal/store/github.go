package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// GitHubJob records an immutable commit before acknowledging its delivery.
// Review and retries always refer to this SHA, including after a restart.
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

func (s *Store) QueueGitHub(ctx context.Context, repoID, installation int64, delivery, branch, sha string, pr int, review bool) error {
	state := "queued"
	if review {
		state = "approval"
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO github_jobs(project,delivery,branch,sha,pr,state)
 SELECT project,?,?,?,?,? FROM github_repos WHERE repo_id=? AND installation=?
 ON CONFLICT(project,delivery) DO NOTHING`, delivery, branch, sha, pr, state, repoID, installation)
	return err
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
func (s *Store) SaveGitHubJob(ctx context.Context, id int64, state, dep, detail string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE github_jobs SET state=?,deployment=?,detail=? WHERE id=?", state, dep, detail, id)
	return err
}
func (s *Store) ResumeGitHub(ctx context.Context, id int64) error {
	r, err := s.db.ExecContext(ctx, "UPDATE github_jobs SET state='queued',detail='' WHERE id=? AND state IN ('approval','error')", id)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return err
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
