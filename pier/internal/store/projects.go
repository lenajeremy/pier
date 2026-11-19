package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Project is a project and how it's built. Empty build fields mean "what
// Pier detects": only what the owner sets overrides detection.
type Project struct {
	Name             string    `json:"name"`
	CreatedAt        time.Time `json:"created_at"`
	ProductionBranch string    `json:"production_branch"` // empty: the server's default
	Repo             string    `json:"repo"`              // owner/repo on GitHub; empty for CLI only
	Build            Build     `json:"build"`
	RolledBackFrom   string    `json:"rolled_back_from"` // what production served before a rollback; empty if not rolled back
}

// Build is the owner's overrides of what Pier detects.
type Build struct {
	Root    string `json:"root"`    // the folder to build in, relative to the project
	Preset  string `json:"preset"`  // "", static or vite
	Install string `json:"install"` // install command
	Command string `json:"command"` // build command
	Output  string `json:"output"`  // the folder that's served
}

// ErrExists means a project with that name already exists.
var ErrExists = errors.New("already exists")

const projectCols = `name, created_at, production_branch, repo, root_dir, preset, install_cmd, build_cmd, output_dir, rolled_back_from`

func scanProject(sc interface{ Scan(...any) error }) (Project, error) {
	var p Project
	var created int64
	err := sc.Scan(&p.Name, &created, &p.ProductionBranch, &p.Repo,
		&p.Build.Root, &p.Build.Preset, &p.Build.Install, &p.Build.Command, &p.Build.Output, &p.RolledBackFrom)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	p.CreatedAt = time.UnixMilli(created)
	return p, err
}

// CreateProject adds a project. It fails with ErrExists if the name is taken.
func (s *Store) CreateProject(ctx context.Context, p Project) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO projects(name, created_at, production_branch, repo, root_dir, preset, install_cmd, build_cmd, output_dir)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.Name, time.Now().UnixMilli(), p.ProductionBranch, p.Repo,
		p.Build.Root, p.Build.Preset, p.Build.Install, p.Build.Command, p.Build.Output)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return ErrExists
	}
	return err
}

// Project returns one project.
func (s *Store) Project(ctx context.Context, name string) (Project, error) {
	return scanProject(s.db.QueryRowContext(ctx, `SELECT `+projectCols+` FROM projects WHERE name = ?`, name))
}

// SetProjectBuild changes a project's production branch and build overrides.
func (s *Store) SetProjectBuild(ctx context.Context, name, productionBranch string, b Build) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE projects SET production_branch = ?, root_dir = ?, preset = ?, install_cmd = ?, build_cmd = ?, output_dir = ?
		WHERE name = ?`,
		productionBranch, b.Root, b.Preset, b.Install, b.Command, b.Output, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetRolledBack records a rollback (from is what production served before
// it) or, with from empty, that production follows its builds again.
func (s *Store) SetRolledBack(ctx context.Context, name, from string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE projects SET rolled_back_from = ? WHERE name = ?`, from, name)
	return err
}
