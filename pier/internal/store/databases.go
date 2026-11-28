package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Database is a project's Postgres server. Its passwords are sealed.
type Database struct {
	Project       string    `json:"project"`
	Image         string    `json:"image"`
	MemoryMB      int       `json:"memory_mb"`
	AdminPassword string    `json:"-"`
	Password      string    `json:"-"`
	CreatedAt     time.Time `json:"created_at"`
	CreatedBy     string    `json:"created_by,omitempty"`
}

// ProjectDatabase is one database on a project's server: the environments
// it serves and the variable they get it as. Name is the database's and its
// role's. Password is sealed.
type ProjectDatabase struct {
	ID           int64     `json:"id"`
	Project      string    `json:"project"`
	Name         string    `json:"name"`
	Environments []string  `json:"environments"`
	Variable     string    `json:"variable"`
	Password     string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
	CreatedBy    string    `json:"created_by,omitempty"`
}

// Serves reports whether the database is linked to a target.
func (d ProjectDatabase) Serves(target string) bool {
	for _, e := range d.Environments {
		if e == target {
			return true
		}
	}
	return false
}

// External is a role that reaches one database from outside Pier, through
// pierd's TLS port. Password is sealed.
type External struct {
	Role      string    `json:"role"`
	Project   string    `json:"project"`
	Database  string    `json:"database"`
	Password  string    `json:"-"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy string    `json:"created_by,omitempty"`
}

// Why a backup was made.
const (
	BackupNightly       = "nightly"
	BackupManual        = "manual"
	BackupBeforeRestore = "before-restore"
	BackupBeforeDelete  = "before-delete"
)

// A backup's states.
const (
	BackupRunning = "running"
	BackupDone    = "done"
	BackupFailed  = "failed"
)

// Backup is one pg_dump of one of a project's databases.
type Backup struct {
	ID         int64     `json:"id"`
	Project    string    `json:"project"`
	Database   string    `json:"database"`
	Reason     string    `json:"reason"`
	MadeBy     string    `json:"made_by,omitempty"`
	State      string    `json:"state"`
	File       string    `json:"-"`
	Bytes      int64     `json:"bytes"`
	Error      string    `json:"error,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at,omitzero"`
	Offsite    string    `json:"offsite,omitempty"` // "sent", or why sending it failed
}

const databaseCols = `project, image, memory_mb, admin_password, password, created_at, created_by`

func scanDatabase(sc interface{ Scan(...any) error }) (Database, error) {
	var d Database
	var created int64
	err := sc.Scan(&d.Project, &d.Image, &d.MemoryMB, &d.AdminPassword, &d.Password, &created, &d.CreatedBy)
	d.CreatedAt = time.UnixMilli(created)
	return d, err
}

// CreateDatabase records a project's new database.
func (s *Store) CreateDatabase(ctx context.Context, d Database) error {
	res, err := s.db.ExecContext(ctx, `INSERT INTO databases(`+databaseCols+`)
		SELECT ?,?,?,?,?,?,? WHERE EXISTS (SELECT 1 FROM projects WHERE name = ?) ON CONFLICT(project) DO NOTHING`,
		d.Project, d.Image, d.MemoryMB, d.AdminPassword, d.Password, d.CreatedAt.UnixMilli(), d.CreatedBy, d.Project)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if _, err := s.Project(ctx, d.Project); err != nil {
			return err
		}
		return ErrExists
	}
	return nil
}

// Database is a project's database, or ErrNotFound.
func (s *Store) Database(ctx context.Context, project string) (Database, error) {
	d, err := scanDatabase(s.db.QueryRowContext(ctx, `SELECT `+databaseCols+` FROM databases WHERE project = ?`, project))
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	return d, err
}

// Databases lists every project's database.
func (s *Store) Databases(ctx context.Context) ([]Database, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+databaseCols+` FROM databases ORDER BY project`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Database
	for rows.Next() {
		d, err := scanDatabase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// SetDatabaseMemory changes a database's memory limit.
func (s *Store) SetDatabaseMemory(ctx context.Context, project string, mb int) error {
	res, err := s.db.ExecContext(ctx, `UPDATE databases SET memory_mb = ? WHERE project = ?`, mb, project)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteDatabase forgets a project's server, its databases and its external URLs.
// Its backups stay listed while the project does.
func (s *Store) DeleteDatabase(ctx context.Context, project string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{`DELETE FROM project_databases WHERE project = ?`, `DELETE FROM database_externals WHERE project = ?`} {
		if _, err := tx.ExecContext(ctx, q, project); err != nil {
			return err
		}
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM databases WHERE project = ?`, project)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

const projectDatabaseCols = `id, project, name, environments, variable, password, created_at, created_by`

func scanProjectDatabase(sc interface{ Scan(...any) error }) (ProjectDatabase, error) {
	var d ProjectDatabase
	var created int64
	var envs string
	err := sc.Scan(&d.ID, &d.Project, &d.Name, &envs, &d.Variable, &d.Password, &created, &d.CreatedBy)
	d.CreatedAt = time.UnixMilli(created)
	d.Environments = splitEnvironments(envs)
	return d, err
}

// AddProjectDatabase records a database on a project's server.
func (s *Store) AddProjectDatabase(ctx context.Context, d ProjectDatabase) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO project_databases(project, name, environments, variable, password, created_at, created_by) VALUES(?,?,?,?,?,?,?)`,
		d.Project, d.Name, strings.Join(d.Environments, ","), d.Variable, d.Password, d.CreatedAt.UnixMilli(), d.CreatedBy)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ProjectDatabase is one of a project's databases by name, or ErrNotFound.
func (s *Store) ProjectDatabase(ctx context.Context, project, name string) (ProjectDatabase, error) {
	d, err := scanProjectDatabase(s.db.QueryRowContext(ctx, `SELECT `+projectDatabaseCols+` FROM project_databases WHERE project = ? AND name = ?`, project, name))
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	return d, err
}

// ProjectDatabases lists the databases on a project's server, oldest first.
func (s *Store) ProjectDatabases(ctx context.Context, project string) ([]ProjectDatabase, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+projectDatabaseCols+` FROM project_databases WHERE project = ? ORDER BY id`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProjectDatabase{}
	for rows.Next() {
		d, err := scanProjectDatabase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// LinkProjectDatabase sets which environments a database serves and the
// variable they get it as.
func (s *Store) LinkProjectDatabase(ctx context.Context, project, name string, environments []string, variable string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE project_databases SET environments = ?, variable = ? WHERE project = ? AND name = ?`,
		strings.Join(environments, ","), variable, project, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteProjectDatabase forgets one of a project's databases and its
// external URLs.
func (s *Store) DeleteProjectDatabase(ctx context.Context, project, name string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM database_externals WHERE project = ? AND database = ?`, project, name); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM project_databases WHERE project = ? AND name = ?`, project, name)
	return err
}

// AddExternal records an external URL's role.
func (s *Store) AddExternal(ctx context.Context, e External) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO database_externals(role, project, database, password, created_at, created_by) VALUES(?,?,?,?,?,?)`,
		e.Role, e.Project, e.Database, e.Password, e.CreatedAt.UnixMilli(), e.CreatedBy)
	return err
}

func scanExternal(sc interface{ Scan(...any) error }) (External, error) {
	var e External
	var created int64
	err := sc.Scan(&e.Role, &e.Project, &e.Database, &e.Password, &created, &e.CreatedBy)
	e.CreatedAt = time.UnixMilli(created)
	return e, err
}

// External is an external URL's role, or ErrNotFound.
func (s *Store) External(ctx context.Context, role string) (External, error) {
	e, err := scanExternal(s.db.QueryRowContext(ctx, `SELECT role, project, database, password, created_at, created_by FROM database_externals WHERE role = ?`, role))
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	return e, err
}

// Externals lists a project's external URLs, oldest first.
func (s *Store) Externals(ctx context.Context, project string) ([]External, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT role, project, database, password, created_at, created_by FROM database_externals WHERE project = ? ORDER BY created_at`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []External{}
	for rows.Next() {
		e, err := scanExternal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// DeleteExternal forgets an external URL's role.
func (s *Store) DeleteExternal(ctx context.Context, project, role string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM database_externals WHERE project = ? AND role = ?`, project, role)
	return err
}

const backupCols = `id, project, database, reason, made_by, state, file, bytes, error, started_at, finished_at, offsite`

func scanBackup(sc interface{ Scan(...any) error }) (Backup, error) {
	var b Backup
	var started, finished int64
	err := sc.Scan(&b.ID, &b.Project, &b.Database, &b.Reason, &b.MadeBy, &b.State, &b.File, &b.Bytes, &b.Error, &started, &finished, &b.Offsite)
	b.StartedAt = time.UnixMilli(started)
	if finished > 0 {
		b.FinishedAt = time.UnixMilli(finished)
	}
	return b, err
}

func (s *Store) queryBackups(ctx context.Context, q string, args ...any) ([]Backup, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+backupCols+` FROM database_backups `+q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Backup
	for rows.Next() {
		b, err := scanBackup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// StartBackup records a backup that has begun.
func (s *Store) StartBackup(ctx context.Context, b Backup) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO database_backups(project, database, reason, made_by, state, started_at) VALUES(?,?,?,?,?,?)`,
		b.Project, b.Database, b.Reason, b.MadeBy, BackupRunning, b.StartedAt.UnixMilli())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// FinishBackup records how a backup ended.
func (s *Store) FinishBackup(ctx context.Context, b Backup) error {
	_, err := s.db.ExecContext(ctx, `UPDATE database_backups SET state = ?, file = ?, bytes = ?, error = ?, finished_at = ? WHERE id = ?`,
		b.State, b.File, b.Bytes, b.Error, b.FinishedAt.UnixMilli(), b.ID)
	return err
}

// SetBackupOffsite records whether a backup reached offsite storage.
func (s *Store) SetBackupOffsite(ctx context.Context, id int64, offsite string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE database_backups SET offsite = ? WHERE id = ?`, offsite, id)
	return err
}

// Backups lists a project's backups, newest first.
func (s *Store) Backups(ctx context.Context, project string) ([]Backup, error) {
	return s.queryBackups(ctx, `WHERE project = ? ORDER BY id DESC`, project)
}

// BackupByID is one of a project's backups, or ErrNotFound.
func (s *Store) BackupByID(ctx context.Context, project string, id int64) (Backup, error) {
	b, err := scanBackup(s.db.QueryRowContext(ctx, `SELECT `+backupCols+` FROM database_backups WHERE project = ? AND id = ?`, project, id))
	if errors.Is(err, sql.ErrNoRows) {
		return b, ErrNotFound
	}
	return b, err
}

// LastBackup is the newest finished backup of one of a project's
// databases made for reason, or ErrNotFound.
func (s *Store) LastBackup(ctx context.Context, project, database, reason string) (Backup, error) {
	b, err := scanBackup(s.db.QueryRowContext(ctx, `SELECT `+backupCols+` FROM database_backups
		WHERE project = ? AND database = ? AND reason = ? AND state = ? ORDER BY id DESC LIMIT 1`, project, database, reason, BackupDone))
	if errors.Is(err, sql.ErrNoRows) {
		return b, ErrNotFound
	}
	return b, err
}

// DeleteBackup forgets a backup.
func (s *Store) DeleteBackup(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM database_backups WHERE id = ?`, id)
	return err
}

// InterruptBackups marks the backups pierd stopped in the middle of as
// failed, when it starts again.
func (s *Store) InterruptBackups(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE database_backups SET state = ?, error = ?, finished_at = ? WHERE state = ?`,
		BackupFailed, "pierd stopped before the backup finished.", time.Now().UnixMilli(), BackupRunning)
	return err
}
