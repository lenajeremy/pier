package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Job kinds.
const (
	JobCommand = "command" // runs a command in a fresh container from production's image
	JobPath    = "path"    // requests a path on production
)

// Job run statuses.
const (
	RunRunning   = "running"
	RunSucceeded = "succeeded"
	RunFailed    = "failed"
	RunSkipped   = "skipped" // the run before it was still going
)

// Who started a run.
const (
	BySchedule = "schedule"
	ByHand     = "manual"
)

// Job is one scheduled job of a project.
type Job struct {
	ID        int64     `json:"id"`
	Project   string    `json:"project"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	Schedule  string    `json:"schedule"`
	Timezone  string    `json:"timezone"`
	Command   string    `json:"command,omitempty"`
	Method    string    `json:"method,omitempty"`
	Path      string    `json:"path,omitempty"`
	TimeoutS  int       `json:"timeout_s"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// JobRun is one run of a job.
type JobRun struct {
	ID           int64     `json:"id"`
	Job          int64     `json:"job"`
	Project      string    `json:"project"`
	StartedBy    string    `json:"started_by"` // schedule or manual
	Status       string    `json:"status"`
	Deployment   string    `json:"deployment,omitempty"` // what production served when it ran
	ScheduledFor time.Time `json:"scheduled_for,omitzero"`
	StartedAt    time.Time `json:"started_at"`
	FinishedAt   time.Time `json:"finished_at,omitzero"`
	ExitCode     *int      `json:"exit_code,omitempty"`
	HTTPStatus   *int      `json:"http_status,omitempty"`
	Detail       string    `json:"detail,omitempty"` // why it failed or was skipped
}

const jobCols = `id, project, name, kind, schedule, timezone, command, method, path, timeout_s, enabled, created_at, updated_at`

func scanScheduledJob(sc interface{ Scan(...any) error }) (Job, error) {
	var j Job
	var created, updated int64
	err := sc.Scan(&j.ID, &j.Project, &j.Name, &j.Kind, &j.Schedule, &j.Timezone, &j.Command, &j.Method, &j.Path, &j.TimeoutS, &j.Enabled, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return j, ErrNotFound
	}
	j.CreatedAt, j.UpdatedAt = time.UnixMilli(created), time.UnixMilli(updated)
	return j, err
}

func (s *Store) queryJobs(ctx context.Context, q string, args ...any) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		j, err := scanScheduledJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// CreateJob adds a job and returns its ID. It fails with ErrExists when
// the project has a job of that name.
func (s *Store) CreateJob(ctx context.Context, j Job) (int64, error) {
	now := time.Now().UnixMilli()
	res, err := s.db.ExecContext(ctx, `INSERT INTO jobs(project, name, kind, schedule, timezone, command, method, path, timeout_s, enabled, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		j.Project, j.Name, j.Kind, j.Schedule, j.Timezone, j.Command, j.Method, j.Path, j.TimeoutS, j.Enabled, now, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return 0, ErrExists
		}
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateJob changes a job.
func (s *Store) UpdateJob(ctx context.Context, j Job) error {
	res, err := s.db.ExecContext(ctx, `UPDATE jobs SET name = ?, kind = ?, schedule = ?, timezone = ?, command = ?, method = ?, path = ?,
		timeout_s = ?, enabled = ?, updated_at = ? WHERE id = ? AND project = ?`,
		j.Name, j.Kind, j.Schedule, j.Timezone, j.Command, j.Method, j.Path, j.TimeoutS, j.Enabled, time.Now().UnixMilli(), j.ID, j.Project)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return ErrExists
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteJob removes a job and its runs.
func (s *Store) DeleteJob(ctx context.Context, project string, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM job_runs WHERE job = ?`, id); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM jobs WHERE id = ? AND project = ?`, id, project)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

// Jobs lists a project's jobs by name.
func (s *Store) Jobs(ctx context.Context, project string) ([]Job, error) {
	return s.queryJobs(ctx, `SELECT `+jobCols+` FROM jobs WHERE project = ? ORDER BY name`, project)
}

// EnabledJobs lists every job that runs on its schedule, on every project.
func (s *Store) EnabledJobs(ctx context.Context) ([]Job, error) {
	return s.queryJobs(ctx, `SELECT `+jobCols+` FROM jobs WHERE enabled = 1 ORDER BY id`)
}

// JobByID returns one of a project's jobs.
func (s *Store) JobByID(ctx context.Context, project string, id int64) (Job, error) {
	return scanScheduledJob(s.db.QueryRowContext(ctx, `SELECT `+jobCols+` FROM jobs WHERE id = ? AND project = ?`, id, project))
}

const runCols = `id, job, project, started_by, status, deployment, scheduled_for, started_at, finished_at, exit_code, http_status, detail`

func scanRun(sc interface{ Scan(...any) error }) (JobRun, error) {
	var r JobRun
	var scheduled, started, finished int64
	var exit, status sql.NullInt64
	err := sc.Scan(&r.ID, &r.Job, &r.Project, &r.StartedBy, &r.Status, &r.Deployment, &scheduled, &started, &finished, &exit, &status, &r.Detail)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	r.ScheduledFor, r.StartedAt, r.FinishedAt = fromMS(scheduled), time.UnixMilli(started), fromMS(finished)
	if exit.Valid {
		v := int(exit.Int64)
		r.ExitCode = &v
	}
	if status.Valid {
		v := int(status.Int64)
		r.HTTPStatus = &v
	}
	return r, err
}

func nullInt(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}

// StartRun records a run that has started, or one that was skipped, and
// returns its ID.
func (s *Store) StartRun(ctx context.Context, r JobRun) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO job_runs(job, project, started_by, status, deployment, scheduled_for, started_at, finished_at, exit_code, http_status, detail)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.Job, r.Project, r.StartedBy, r.Status, r.Deployment, ms(r.ScheduledFor), r.StartedAt.UnixMilli(), ms(r.FinishedAt), nullInt(r.ExitCode), nullInt(r.HTTPStatus), r.Detail)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// FinishRun records how a run ended.
func (s *Store) FinishRun(ctx context.Context, r JobRun) error {
	_, err := s.db.ExecContext(ctx, `UPDATE job_runs SET status = ?, deployment = ?, finished_at = ?, exit_code = ?, http_status = ?, detail = ? WHERE id = ?`,
		r.Status, r.Deployment, ms(r.FinishedAt), nullInt(r.ExitCode), nullInt(r.HTTPStatus), r.Detail, r.ID)
	return err
}

// Run returns one run of a project's job.
func (s *Store) Run(ctx context.Context, project string, job, id int64) (JobRun, error) {
	return scanRun(s.db.QueryRowContext(ctx, `SELECT `+runCols+` FROM job_runs WHERE id = ? AND job = ? AND project = ?`, id, job, project))
}

// Runs lists a page of a job's runs, newest first, and how many it has.
func (s *Store) Runs(ctx context.Context, job int64, offset, limit int) ([]JobRun, int, error) {
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM job_runs WHERE job = ?`, job).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+runCols+` FROM job_runs WHERE job = ? ORDER BY id DESC LIMIT ? OFFSET ?`, job, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []JobRun{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// LastRuns returns each of a project's jobs' newest run that wasn't
// skipped, by job.
func (s *Store) LastRuns(ctx context.Context, project string) (map[int64]JobRun, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+runCols+` FROM job_runs r WHERE project = ? AND id = (
		SELECT MAX(id) FROM job_runs WHERE job = r.job AND status != ?)`, project, RunSkipped)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]JobRun{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out[r.Job] = r
	}
	return out, rows.Err()
}

// InterruptRuns ends the runs pierd was in the middle of when it stopped.
func (s *Store) InterruptRuns(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE job_runs SET status = ?, finished_at = ?, detail = 'pierd stopped during this run.' WHERE status = ?`,
		RunFailed, time.Now().UnixMilli(), RunRunning)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PruneRuns forgets runs that started before a time, and returns them, so
// their logs can go too.
func (s *Store) PruneRuns(ctx context.Context, before time.Time) ([]JobRun, error) {
	rows, err := s.db.QueryContext(ctx, `DELETE FROM job_runs WHERE started_at < ? AND status != ? RETURNING `+runCols, before.UnixMilli(), RunRunning)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []JobRun
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CronSecret returns a project's sealed cron secret and when it was made,
// or "" before there is one.
func (s *Store) CronSecret(ctx context.Context, project string) (string, time.Time, error) {
	var sealed string
	var at int64
	err := s.db.QueryRowContext(ctx, `SELECT cron_secret, cron_secret_at FROM projects WHERE name = ?`, project).Scan(&sealed, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, ErrNotFound
	}
	return sealed, fromMS(at), err
}

// SetCronSecret keeps a project's first cron secret. One that's already
// set stays: two builds at once mustn't each make their own.
func (s *Store) SetCronSecret(ctx context.Context, project, sealed string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE projects SET cron_secret = ?, cron_secret_at = ? WHERE name = ? AND cron_secret = ''`,
		sealed, time.Now().UnixMilli(), project)
	return err
}
