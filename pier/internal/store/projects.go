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
	Runtime          Runtime   `json:"runtime"`
	RolledBackFrom   string    `json:"rolled_back_from"` // what production served before a rollback; empty if not rolled back
}

// Build is the owner's overrides of what Pier detects.
type Build struct {
	Root    string `json:"root"`    // the folder to build in, relative to the project
	Preset  string `json:"preset"`  // "", static, vite, server or dockerfile
	Install string `json:"install"` // install command
	Command string `json:"command"` // build command
	Output  string `json:"output"`  // the folder that's served
}

// Runtime is the owner's container runtime overrides for server apps.
// MemoryMB 0 uses Pier's default cap, negative disables the cap, positive sets MB.
type Runtime struct {
	StartCommand string  `json:"start_command"`
	HealthPath   string  `json:"health_path"`
	MemoryMB     int     `json:"memory_mb"`
	CPUs         float64 `json:"cpus"`
}

// ErrExists means a project with that name already exists.
var ErrExists = errors.New("already exists")

const projectCols = `name, created_at, production_branch, repo, root_dir, preset, install_cmd, build_cmd, output_dir, start_cmd, health_path, memory_mb, cpus, rolled_back_from`

func scanProject(sc interface{ Scan(...any) error }) (Project, error) {
	var p Project
	var created int64
	err := sc.Scan(&p.Name, &created, &p.ProductionBranch, &p.Repo,
		&p.Build.Root, &p.Build.Preset, &p.Build.Install, &p.Build.Command, &p.Build.Output,
		&p.Runtime.StartCommand, &p.Runtime.HealthPath, &p.Runtime.MemoryMB, &p.Runtime.CPUs, &p.RolledBackFrom)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	p.CreatedAt = time.UnixMilli(created)
	return p, err
}

// CreateProject adds a project. It fails with ErrExists if the name is taken.
func (s *Store) CreateProject(ctx context.Context, p Project) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO projects(name, created_at, production_branch, repo, root_dir, preset, install_cmd, build_cmd, output_dir, start_cmd, health_path, memory_mb, cpus)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.Name, time.Now().UnixMilli(), p.ProductionBranch, p.Repo,
		p.Build.Root, p.Build.Preset, p.Build.Install, p.Build.Command, p.Build.Output,
		p.Runtime.StartCommand, p.Runtime.HealthPath, p.Runtime.MemoryMB, p.Runtime.CPUs)
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
	p, err := s.Project(ctx, name)
	if err != nil {
		return err
	}
	return s.SetProjectSettings(ctx, name, productionBranch, b, p.Runtime)
}

// SetProjectSettings changes a project's production branch, build overrides, and runtime settings.
func (s *Store) SetProjectSettings(ctx context.Context, name, productionBranch string, b Build, rt Runtime) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE projects SET production_branch = ?, root_dir = ?, preset = ?, install_cmd = ?, build_cmd = ?, output_dir = ?, start_cmd = ?, health_path = ?, memory_mb = ?, cpus = ?
		WHERE name = ?`,
		productionBranch, b.Root, b.Preset, b.Install, b.Command, b.Output, rt.StartCommand, rt.HealthPath, rt.MemoryMB, rt.CPUs, name)
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
