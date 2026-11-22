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

// Deployment targets.
const (
	TargetProduction = "production"
	TargetPreview    = "preview"
)

// Deployment sources.
const (
	SourceGit    = "git" // a git push
	SourceGitHub = "github"
	SourceCLI    = "cli" // a folder uploaded by pier deploy
)

type Deployment struct {
	ID        string    `json:"id"`
	Project   string    `json:"project"`
	Branch    string    `json:"branch"`
	Commit    string    `json:"commit"`
	Message   string    `json:"message"`
	Status    string    `json:"status"`
	Framework string    `json:"framework"`
	Runtime   string    `json:"runtime,omitempty"`
	Files     int       `json:"files"`
	Bytes     int64     `json:"bytes"`
	BuildMS   int64     `json:"build_ms"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Target    string    `json:"target"`         // production or preview
	Source    string    `json:"source"`         // git, github or cli
	Plan      string    `json:"plan,omitempty"` // JSON: what the build ran
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

	// 2: projects are created first, with their own build settings; each
	// deployment says whether it was for production or a preview, where it
	// came from, and what its build ran.
	`ALTER TABLE projects ADD COLUMN production_branch TEXT NOT NULL DEFAULT '';
	ALTER TABLE projects ADD COLUMN repo        TEXT NOT NULL DEFAULT '';
	ALTER TABLE projects ADD COLUMN root_dir    TEXT NOT NULL DEFAULT '';
	ALTER TABLE projects ADD COLUMN preset      TEXT NOT NULL DEFAULT '';
	ALTER TABLE projects ADD COLUMN install_cmd TEXT NOT NULL DEFAULT '';
	ALTER TABLE projects ADD COLUMN build_cmd   TEXT NOT NULL DEFAULT '';
	ALTER TABLE projects ADD COLUMN output_dir  TEXT NOT NULL DEFAULT '';
	ALTER TABLE deployments ADD COLUMN target TEXT NOT NULL DEFAULT '';
	ALTER TABLE deployments ADD COLUMN source TEXT NOT NULL DEFAULT 'git';
	ALTER TABLE deployments ADD COLUMN plan   TEXT NOT NULL DEFAULT '';`,

	// 3: a rollback remembers what production served before it, so it can
	// be undone, and so production builds don't go live until then.
	`ALTER TABLE projects ADD COLUMN rolled_back_from TEXT NOT NULL DEFAULT '';`,

	// 4: environment variables, sealed, one row per scope; and when each
	// scope last changed, to tell whether a deployment has the latest.
	`CREATE TABLE variables (
		project    TEXT NOT NULL REFERENCES projects(name),
		scope      TEXT NOT NULL,
		name       TEXT NOT NULL,
		value      TEXT NOT NULL,
		updated_at INTEGER NOT NULL,
		PRIMARY KEY (project, scope, name)
	);
	ALTER TABLE projects ADD COLUMN vars_changed_production INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE projects ADD COLUMN vars_changed_preview    INTEGER NOT NULL DEFAULT 0;`,
	`CREATE TABLE github_repos (
		project TEXT PRIMARY KEY REFERENCES projects(name),
		repo_id INTEGER NOT NULL UNIQUE,
		installation INTEGER NOT NULL
	);
	CREATE TABLE github_jobs (
		id INTEGER PRIMARY KEY,
		project TEXT NOT NULL REFERENCES github_repos(project),
		delivery TEXT NOT NULL, branch TEXT NOT NULL, sha TEXT NOT NULL,
		pr INTEGER NOT NULL, state TEXT NOT NULL, deployment TEXT NOT NULL DEFAULT '',
		detail TEXT NOT NULL DEFAULT '', UNIQUE(project,delivery)
	);
	CREATE INDEX github_jobs_state ON github_jobs(state,id);`,
	`CREATE TABLE custom_domains (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		project TEXT NOT NULL REFERENCES projects(name),
		host TEXT NOT NULL UNIQUE,
		redirect TEXT NOT NULL,
		verified INTEGER NOT NULL DEFAULT 0,
		checked INTEGER NOT NULL DEFAULT 0,
		address_result TEXT NOT NULL DEFAULT '',
		redirect_result TEXT NOT NULL DEFAULT ''
	);`,
	`CREATE TABLE shared_variables (
		name TEXT PRIMARY KEY, value TEXT NOT NULL, production INTEGER NOT NULL, preview INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE TABLE shared_links (
		name TEXT NOT NULL REFERENCES shared_variables(name) ON DELETE CASCADE,
		project TEXT NOT NULL REFERENCES projects(name), PRIMARY KEY(name,project)
	);
	CREATE INDEX shared_project ON shared_links(project);
	CREATE TABLE activity(id INTEGER PRIMARY KEY, at INTEGER NOT NULL, project TEXT NOT NULL DEFAULT '', title TEXT NOT NULL, detail TEXT NOT NULL DEFAULT '', deployment TEXT NOT NULL DEFAULT '');
	CREATE INDEX activity_time ON activity(at DESC,id DESC);
	INSERT INTO activity(at,project,title) SELECT created_at,name,'Created project' FROM projects;
	INSERT INTO activity(at,project,title,detail,deployment) SELECT created_at+build_ms,project,
		CASE WHEN status='failed' THEN 'Build failed' ELSE 'Build ready' END,message,id FROM deployments WHERE status IN ('ready','failed');
	CREATE TRIGGER activity_project AFTER INSERT ON projects BEGIN
		INSERT INTO activity(at,project,title) VALUES(NEW.created_at,NEW.name,'Created project'); END;
	CREATE TRIGGER activity_deploy AFTER UPDATE OF status ON deployments WHEN NEW.status IN ('ready','failed') AND OLD.status!=NEW.status BEGIN
		INSERT INTO activity(at,project,title,detail,deployment) VALUES(CAST(unixepoch('subsec')*1000 AS INTEGER),NEW.project,CASE NEW.status WHEN 'failed' THEN 'Build failed' ELSE 'Build ready' END,NEW.message,NEW.id); END;
	CREATE TRIGGER activity_session AFTER INSERT ON sessions BEGIN
		INSERT INTO activity(at,title,detail) VALUES(NEW.created_at,CASE NEW.kind WHEN 'cli' THEN 'Approved a computer' ELSE 'Signed in' END,NEW.name); END;
	CREATE TABLE cleanup_files(id TEXT PRIMARY KEY);
 CREATE TRIGGER pending_cleanup BEFORE INSERT ON deployments WHEN EXISTS(SELECT 1 FROM cleanup_files WHERE id=NEW.id) BEGIN SELECT RAISE(ABORT,'deployment cleanup pending'); END;
 ALTER TABLE deployments ADD COLUMN keep_history INTEGER NOT NULL DEFAULT 0;
	UPDATE deployments SET keep_history=1;
	CREATE TRIGGER keep_production_insert AFTER INSERT ON aliases WHEN NEW.kind='production' BEGIN UPDATE deployments SET keep_history=1 WHERE id=NEW.deployment_id; END;
	CREATE TRIGGER keep_production_update AFTER UPDATE ON aliases WHEN NEW.kind='production' BEGIN UPDATE deployments SET keep_history=1 WHERE id=NEW.deployment_id; END;`,
	`CREATE TABLE server_samples(at INTEGER PRIMARY KEY, cpu REAL NOT NULL, memory REAL NOT NULL, disk REAL NOT NULL);`,
	`CREATE TABLE app_resources(deployment TEXT PRIMARY KEY, name TEXT UNIQUE NOT NULL);`,
	`ALTER TABLE projects ADD COLUMN start_cmd   TEXT NOT NULL DEFAULT '';
	ALTER TABLE projects ADD COLUMN health_path TEXT NOT NULL DEFAULT '';
	ALTER TABLE projects ADD COLUMN memory_mb   INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE projects ADD COLUMN cpus        REAL NOT NULL DEFAULT 0;`,

	// 11: one project's activity, newest first, straight from an index.
	// activity_time orders everything; with a project chosen it could only
	// be walked until enough of that project's rows turned up.
	`CREATE INDEX activity_by_project ON activity(project, at DESC, id DESC);`,

	// 12: how many times pierd stopped during a job's build, so a build that
	// stops it every time is given up on; and, for a pull request, when GitHub
	// last updated it as of the delivery (Unix milliseconds), because GitHub
	// can deliver an older push after a newer one.
	`ALTER TABLE github_jobs ADD COLUMN interrupted INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE github_jobs ADD COLUMN pr_updated  INTEGER NOT NULL DEFAULT 0;`,

	// 13: a repository can hold several projects, each built from its own
	// folder, so a repository no longer belongs to one project. SQLite can't
	// drop a constraint, so github_repos is built again without it. Its jobs
	// are moved to a new table first: with foreign keys on, a table that rows
	// still refer to can't be dropped, even for a moment.
	`CREATE TABLE github_repos_new (
		project TEXT PRIMARY KEY REFERENCES projects(name),
		repo_id INTEGER NOT NULL,
		installation INTEGER NOT NULL
	);
	INSERT INTO github_repos_new(project,repo_id,installation) SELECT project,repo_id,installation FROM github_repos;
	CREATE TABLE github_jobs_new (
		id INTEGER PRIMARY KEY,
		project TEXT NOT NULL REFERENCES github_repos_new(project),
		delivery TEXT NOT NULL, branch TEXT NOT NULL, sha TEXT NOT NULL,
		pr INTEGER NOT NULL, state TEXT NOT NULL, deployment TEXT NOT NULL DEFAULT '',
		detail TEXT NOT NULL DEFAULT '', interrupted INTEGER NOT NULL DEFAULT 0,
		pr_updated INTEGER NOT NULL DEFAULT 0, UNIQUE(project,delivery)
	);
	INSERT INTO github_jobs_new(id,project,delivery,branch,sha,pr,state,deployment,detail,interrupted,pr_updated)
		SELECT id,project,delivery,branch,sha,pr,state,deployment,detail,interrupted,pr_updated FROM github_jobs;
	DROP TABLE github_jobs;
	DROP TABLE github_repos;
	ALTER TABLE github_repos_new RENAME TO github_repos;
	ALTER TABLE github_jobs_new RENAME TO github_jobs;
	CREATE INDEX github_repos_by_repo ON github_repos(repo_id);
	CREATE INDEX github_jobs_state ON github_jobs(state,id);`,

	// 14: a project built from one folder of its repository skips the pushes
	// that change nothing it builds from, unless its owner has it build every
	// push.
	`ALTER TABLE projects ADD COLUMN always_build INTEGER NOT NULL DEFAULT 0;`,

	// 15: the name a project's production address has under the base domain,
	// when the owner changed it from the project's name.
	`ALTER TABLE projects ADD COLUMN subdomain TEXT NOT NULL DEFAULT '';`,

	// 16: kept for teams, which another branch adds. When the two meet, its
	// migration takes this place and this one goes.
	`SELECT 1;`,

	// 17: where alerts go, with their secrets sealed; the problems an alert
	// has gone out for and nobody has seen fixed yet, so each is sent once;
	// and who has turned alert emails off (everyone gets them until then).
	`CREATE TABLE alert_channels (
		kind          TEXT PRIMARY KEY,
		config        TEXT NOT NULL DEFAULT '{}',
		secret        TEXT NOT NULL DEFAULT '',
		updated_at    INTEGER NOT NULL,
		last_sent     INTEGER NOT NULL DEFAULT 0,
		last_error    TEXT NOT NULL DEFAULT '',
		last_error_at INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE alert_incidents (
		key       TEXT PRIMARY KEY,
		project   TEXT NOT NULL DEFAULT '',
		opened_at INTEGER NOT NULL,
		last_at   INTEGER NOT NULL,
		count     INTEGER NOT NULL DEFAULT 1
	);
	CREATE TABLE alert_email_off (
		user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE
	);`,

	// 18: a project can turn its health check's request off, so only its
	// process is watched; and how production's server did at its last
	// health check, kept so a restart of pierd doesn't forget a failure.
	`ALTER TABLE projects ADD COLUMN health_off INTEGER NOT NULL DEFAULT 0;
	CREATE TABLE health (
		deployment      TEXT PRIMARY KEY,
		project         TEXT NOT NULL,
		state           TEXT NOT NULL,
		asked           TEXT NOT NULL DEFAULT '',
		failures        INTEGER NOT NULL DEFAULT 0,
		checked_at      INTEGER NOT NULL DEFAULT 0,
		since           INTEGER NOT NULL DEFAULT 0,
		last_failure    TEXT NOT NULL DEFAULT '',
		last_failure_at INTEGER NOT NULL DEFAULT 0,
		restarts        INTEGER NOT NULL DEFAULT 0,
		restarted_at    INTEGER NOT NULL DEFAULT 0,
		next_restart    INTEGER NOT NULL DEFAULT 0
	);`,
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

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

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
	return insertDeployment(ctx, s.db, d)
}

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func insertDeployment(ctx context.Context, db execer, d Deployment) error {
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now()
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO deployments(id, project, branch, commit_sha, message, status, created_at, target, source)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.Project, d.Branch, d.Commit, d.Message, d.Status, d.CreatedAt.UnixMilli(), d.Target, orDefault(d.Source, SourceGit))
	return err
}

// FinishDeployment records how a build ended.
func (s *Store) FinishDeployment(ctx context.Context, d Deployment) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE deployments SET status = ?, framework = ?, files = ?, bytes = ?, build_ms = ?, error = ?, plan = ?
		 WHERE id = ?`,
		d.Status, d.Framework, d.Files, d.Bytes, d.BuildMS, d.Error, d.Plan, d.ID)
	return err
}

const deploymentCols = `id, project, branch, commit_sha, message, status, framework, files, bytes, build_ms, error, created_at, target, source, plan, COALESCE((SELECT r.name FROM app_resources r WHERE r.deployment=id),'')`

func scanDeployment(sc interface{ Scan(...any) error }) (Deployment, error) {
	var d Deployment
	var created int64
	err := sc.Scan(&d.ID, &d.Project, &d.Branch, &d.Commit, &d.Message, &d.Status,
		&d.Framework, &d.Files, &d.Bytes, &d.BuildMS, &d.Error, &created, &d.Target, &d.Source, &d.Plan, &d.Runtime)
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
