package store

import (
	"context"
	"database/sql"
	"errors"
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

// PreviewDatabase is the empty database a project's previews of one branch
// use, on the project's server. Name is the database's and its role's.
type PreviewDatabase struct {
	Project   string    `json:"-"`
	Branch    string    `json:"branch"`
	Name      string    `json:"name"`
	Password  string    `json:"-"`
	CreatedAt time.Time `json:"created_at"`
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

// Backup is one pg_dump of a project's production database.
type Backup struct {
	ID         int64     `json:"id"`
	Project    string    `json:"project"`
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

// DeleteDatabase forgets a project's database and its preview databases.
// Its backups stay listed while the project does.
func (s *Store) DeleteDatabase(ctx context.Context, project string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM preview_databases WHERE project = ?`, project); err != nil {
		return err
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

// PreviewDatabase is the database a branch's previews use, or ErrNotFound.
func (s *Store) PreviewDatabase(ctx context.Context, project, branch string) (PreviewDatabase, error) {
	var p PreviewDatabase
	var created int64
	err := s.db.QueryRowContext(ctx, `SELECT project, branch, name, password, created_at FROM preview_databases WHERE project = ? AND branch = ?`,
		project, branch).Scan(&p.Project, &p.Branch, &p.Name, &p.Password, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	p.CreatedAt = time.UnixMilli(created)
	return p, err
}

// PreviewDatabases lists a project's preview databases, newest first.
func (s *Store) PreviewDatabases(ctx context.Context, project string) ([]PreviewDatabase, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT project, branch, name, password, created_at FROM preview_databases WHERE project = ? ORDER BY created_at DESC`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PreviewDatabase
	for rows.Next() {
		var p PreviewDatabase
		var created int64
		if err := rows.Scan(&p.Project, &p.Branch, &p.Name, &p.Password, &created); err != nil {
			return nil, err
		}
		p.CreatedAt = time.UnixMilli(created)
		out = append(out, p)
	}
	return out, rows.Err()
}

// AddPreviewDatabase records a branch's preview database.
func (s *Store) AddPreviewDatabase(ctx context.Context, p PreviewDatabase) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO preview_databases(project, branch, name, password, created_at) VALUES(?,?,?,?,?)`,
		p.Project, p.Branch, p.Name, p.Password, p.CreatedAt.UnixMilli())
	return err
}

// DeletePreviewDatabase forgets a branch's preview database.
func (s *Store) DeletePreviewDatabase(ctx context.Context, project, branch string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM preview_databases WHERE project = ? AND branch = ?`, project, branch)
	return err
}

const backupCols = `id, project, reason, made_by, state, file, bytes, error, started_at, finished_at, offsite`

func scanBackup(sc interface{ Scan(...any) error }) (Backup, error) {
	var b Backup
	var started, finished int64
	err := sc.Scan(&b.ID, &b.Project, &b.Reason, &b.MadeBy, &b.State, &b.File, &b.Bytes, &b.Error, &started, &finished, &b.Offsite)
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
	res, err := s.db.ExecContext(ctx, `INSERT INTO database_backups(project, reason, made_by, state, started_at) VALUES(?,?,?,?,?)`,
		b.Project, b.Reason, b.MadeBy, BackupRunning, b.StartedAt.UnixMilli())
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

// LastBackup is a project's newest finished backup made for reason, or
// ErrNotFound.
func (s *Store) LastBackup(ctx context.Context, project, reason string) (Backup, error) {
	b, err := scanBackup(s.db.QueryRowContext(ctx, `SELECT `+backupCols+` FROM database_backups
		WHERE project = ? AND reason = ? AND state = ? ORDER BY id DESC LIMIT 1`, project, reason, BackupDone))
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
