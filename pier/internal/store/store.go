// Package store keeps pierd's state in one SQLite file: projects, the
// deployments built for them, and the addresses (aliases) that point at
// deployments. Deployments never change once built; moving an alias is all a
// promote or a rollback does.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Deployment statuses.
const (
	Building = "building"
	Ready    = "ready"
	Failed   = "failed"
)

// Alias kinds.
const (
	KindProduction = "production"
	KindBranch     = "branch"
)

type Deployment struct {
	ID        string    `json:"id"`
	Project   string    `json:"project"`
	Branch    string    `json:"branch"`
	Commit    string    `json:"commit"`
	Message   string    `json:"message"`
	Status    string    `json:"status"`
	Framework string    `json:"framework"`
	Files     int       `json:"files"`
	Bytes     int64     `json:"bytes"`
	BuildMS   int64     `json:"build_ms"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type Alias struct {
	Host         string    `json:"host"`
	Project      string    `json:"project"`
	Kind         string    `json:"kind"`
	Branch       string    `json:"branch"`
	DeploymentID string    `json:"deployment_id"`
	UpdatedAt    time.Time `json:"updated_at"`
}

var ErrNotFound = errors.New("not found")

type Store struct{ db *sql.DB }

const schema = `
CREATE TABLE IF NOT EXISTS projects (
	name       TEXT PRIMARY KEY,
	created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS deployments (
	id         TEXT PRIMARY KEY,
	project    TEXT NOT NULL REFERENCES projects(name),
	branch     TEXT NOT NULL,
	commit_sha TEXT NOT NULL,
	message    TEXT NOT NULL DEFAULT '',
	status     TEXT NOT NULL,
	framework  TEXT NOT NULL DEFAULT '',
	files      INTEGER NOT NULL DEFAULT 0,
	bytes      INTEGER NOT NULL DEFAULT 0,
	build_ms   INTEGER NOT NULL DEFAULT 0,
	error      TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS deployments_by_branch ON deployments(project, branch, created_at);
CREATE TABLE IF NOT EXISTS aliases (
	host          TEXT PRIMARY KEY,
	project       TEXT NOT NULL REFERENCES projects(name),
	kind          TEXT NOT NULL,
	branch        TEXT NOT NULL,
	deployment_id TEXT NOT NULL REFERENCES deployments(id),
	updated_at    INTEGER NOT NULL
);
`

// migrations run in order, once each, after the base schema. The database's
// user_version records how many have run. Only ever append to this list.
var migrations = []string{
	// 1: the server's settings, the owner's account, and signed-in sessions.
	`CREATE TABLE settings (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);
	CREATE TABLE users (
		id            INTEGER PRIMARY KEY,
		email         TEXT NOT NULL UNIQUE COLLATE NOCASE,
		name          TEXT NOT NULL DEFAULT '',
		password_hash TEXT NOT NULL,
		failed_logins INTEGER NOT NULL DEFAULT 0,
		locked_until  INTEGER NOT NULL DEFAULT 0,
		created_at    INTEGER NOT NULL
	);
	CREATE TABLE sessions (
		token_hash   TEXT PRIMARY KEY,
		user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		kind         TEXT NOT NULL,
		name         TEXT NOT NULL DEFAULT '',
		ip           TEXT NOT NULL DEFAULT '',
		created_at   INTEGER NOT NULL,
		last_seen_at INTEGER NOT NULL,
		expires_at   INTEGER NOT NULL
	);
	CREATE INDEX sessions_by_user ON sessions(user_id);`,
}

// Open opens (and if needed creates) the database at path.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // one writer; SQLite serialises anyway
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func migrate(db *sql.DB) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	for i := version; i < len(migrations); i++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		// PRAGMA doesn't take parameters; i is our own integer.
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

// EnsureProject creates the project if it doesn't exist yet.
func (s *Store) EnsureProject(ctx context.Context, name string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO projects(name, created_at) VALUES(?, ?) ON CONFLICT(name) DO NOTHING`,
		name, time.Now().UnixMilli())
	return err
}

// Projects lists project names, oldest first.
func (s *Store) Projects(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name FROM projects ORDER BY created_at, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// CreateDeployment records a deployment that has started building.
func (s *Store) CreateDeployment(ctx context.Context, d Deployment) error {
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO deployments(id, project, branch, commit_sha, message, status, created_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.Project, d.Branch, d.Commit, d.Message, d.Status, d.CreatedAt.UnixMilli())
	return err
}

// FinishDeployment records how a build ended.
func (s *Store) FinishDeployment(ctx context.Context, d Deployment) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE deployments SET status = ?, framework = ?, files = ?, bytes = ?, build_ms = ?, error = ?
		 WHERE id = ?`,
		d.Status, d.Framework, d.Files, d.Bytes, d.BuildMS, d.Error, d.ID)
	return err
}

const deploymentCols = `id, project, branch, commit_sha, message, status, framework, files, bytes, build_ms, error, created_at`

func scanDeployment(sc interface{ Scan(...any) error }) (Deployment, error) {
	var d Deployment
	var created int64
	err := sc.Scan(&d.ID, &d.Project, &d.Branch, &d.Commit, &d.Message, &d.Status,
		&d.Framework, &d.Files, &d.Bytes, &d.BuildMS, &d.Error, &created)
	d.CreatedAt = time.UnixMilli(created)
	return d, err
}

func (s *Store) Deployment(ctx context.Context, id string) (Deployment, error) {
	d, err := scanDeployment(s.db.QueryRowContext(ctx,
		`SELECT `+deploymentCols+` FROM deployments WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	return d, err
}

func (s *Store) queryDeployments(ctx context.Context, q string, args ...any) ([]Deployment, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Deployment
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Deployments lists a project's deployments, newest first.
func (s *Store) Deployments(ctx context.Context, project string) ([]Deployment, error) {
	return s.queryDeployments(ctx,
		`SELECT `+deploymentCols+` FROM deployments WHERE project = ? ORDER BY created_at DESC, id`, project)
}

// ReadyDeployments lists every deployment that built, for routing.
func (s *Store) ReadyDeployments(ctx context.Context) ([]Deployment, error) {
	return s.queryDeployments(ctx,
		`SELECT `+deploymentCols+` FROM deployments WHERE status = ? ORDER BY created_at, id`, Ready)
}

// LatestPerBranch returns the newest deployment of every project and branch.
func (s *Store) LatestPerBranch(ctx context.Context) ([]Deployment, error) {
	return s.queryDeployments(ctx, `
		SELECT `+deploymentCols+` FROM deployments d
		WHERE created_at = (SELECT MAX(created_at) FROM deployments
		                    WHERE project = d.project AND branch = d.branch)
		ORDER BY project, branch`)
}

// SetAlias points host at a deployment, creating the alias if needed.
func (s *Store) SetAlias(ctx context.Context, a Alias) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO aliases(host, project, kind, branch, deployment_id, updated_at)
		VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT(host) DO UPDATE SET deployment_id = excluded.deployment_id,
		                                updated_at = excluded.updated_at`,
		a.Host, a.Project, a.Kind, a.Branch, a.DeploymentID, time.Now().UnixMilli())
	return err
}

// Aliases lists every alias, by project then host.
func (s *Store) Aliases(ctx context.Context) ([]Alias, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT host, project, kind, branch, deployment_id, updated_at FROM aliases ORDER BY project, host`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Alias
	for rows.Next() {
		var a Alias
		var updated int64
		if err := rows.Scan(&a.Host, &a.Project, &a.Kind, &a.Branch, &a.DeploymentID, &updated); err != nil {
			return nil, err
		}
		a.UpdatedAt = time.UnixMilli(updated)
		out = append(out, a)
	}
	return out, rows.Err()
}
