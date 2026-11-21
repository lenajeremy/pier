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
	Subdomain        string    `json:"subdomain"`        // production's name under the base domain; empty: the project's name
}

// Label is the name production's address has under the base domain.
// Previews and each deployment's own address keep the project's name.
func (p Project) Label() string {
	if p.Subdomain != "" {
		return p.Subdomain
	}
	return p.Name
}

// Build is the owner's overrides of what Pier detects.
type Build struct {
	Root    string `json:"root"`    // the folder to build in, relative to the project
	Preset  string `json:"preset"`  // "", static, vite, server or dockerfile
	Install string `json:"install"` // install command
	Command string `json:"command"` // build command
	Output  string `json:"output"`  // the folder that's served
	// AlwaysBuild builds every push. Without it, a project built from a
	// folder of its repository skips a push that changes nothing it builds
	// from.
	AlwaysBuild bool `json:"always_build"`
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

// ErrBuilding means a project cannot be deleted while a build is running.
var ErrBuilding = errors.New("build in progress")

const projectCols = `name, created_at, production_branch, repo, root_dir, preset, install_cmd, build_cmd, output_dir, start_cmd, health_path, memory_mb, cpus, rolled_back_from, always_build, subdomain`

func scanProject(sc interface{ Scan(...any) error }) (Project, error) {
	var p Project
	var created int64
	err := sc.Scan(&p.Name, &created, &p.ProductionBranch, &p.Repo,
		&p.Build.Root, &p.Build.Preset, &p.Build.Install, &p.Build.Command, &p.Build.Output,
		&p.Runtime.StartCommand, &p.Runtime.HealthPath, &p.Runtime.MemoryMB, &p.Runtime.CPUs, &p.RolledBackFrom, &p.Build.AlwaysBuild, &p.Subdomain)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	p.CreatedAt = time.UnixMilli(created)
	return p, err
}

// CreateProject adds a project. It fails with ErrExists if the name is taken.
func (s *Store) CreateProject(ctx context.Context, p Project) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO projects(name, created_at, production_branch, repo, root_dir, preset, install_cmd, build_cmd, output_dir, start_cmd, health_path, memory_mb, cpus, always_build)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.Name, time.Now().UnixMilli(), p.ProductionBranch, p.Repo,
		p.Build.Root, p.Build.Preset, p.Build.Install, p.Build.Command, p.Build.Output,
		p.Runtime.StartCommand, p.Runtime.HealthPath, p.Runtime.MemoryMB, p.Runtime.CPUs, p.Build.AlwaysBuild)
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
		UPDATE projects SET production_branch = ?, root_dir = ?, preset = ?, install_cmd = ?, build_cmd = ?, output_dir = ?, start_cmd = ?, health_path = ?, memory_mb = ?, cpus = ?, always_build = ?
		WHERE name = ?`,
		productionBranch, b.Root, b.Preset, b.Install, b.Command, b.Output, rt.StartCommand, rt.HealthPath, rt.MemoryMB, rt.CPUs, b.AlwaysBuild, name)
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

// DeleteProject removes a project and all of its database rows atomically,
// queuing every deployment's artifacts into cleanup_files so containers,
// images, logs, and saved sources are reclaimed cleanly even across restarts.
func (s *Store) DeleteProject(ctx context.Context, name string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM projects WHERE name = ?`, name).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return ErrNotFound
	}
	var building int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM deployments WHERE project = ? AND status = ?`, name, Building).Scan(&building); err != nil {
		return err
	}
	if building > 0 {
		return ErrBuilding
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO cleanup_files(id) SELECT id FROM deployments WHERE project = ?`, name); err != nil {
		return err
	}
	for _, stmt := range []string{
		`DELETE FROM aliases WHERE project = ?`,
		`DELETE FROM github_jobs WHERE project = ?`,
		`DELETE FROM github_repos WHERE project = ?`,
		`DELETE FROM custom_domains WHERE project = ?`,
		`DELETE FROM variables WHERE project = ?`,
		`DELETE FROM shared_links WHERE project = ?`,
		`DELETE FROM deployments WHERE project = ?`,
		`DELETE FROM projects WHERE name = ?`,
	} {
		if _, err := tx.ExecContext(ctx, stmt, name); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ErrTaken means another project, or a custom domain, already has that
// address.
var ErrTaken = errors.New("address taken")

// SetSubdomain renames production's address under the base domain: the
// project's own name when sub is empty. host is the address it becomes.
// Production's alias moves with it in the same transaction, so the router
// never serves both or neither. A name another project goes by, as its name
// or its address, or a custom domain's, is ErrTaken.
func (s *Store) SetSubdomain(ctx context.Context, project, sub, host string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	label := sub
	if label == "" {
		label = project
	}
	var taken int
	err = tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM projects WHERE name<>? AND (name=? OR subdomain=?))
		+ (SELECT COUNT(*) FROM custom_domains WHERE host=? OR redirect=?)
		+ (SELECT COUNT(*) FROM aliases WHERE host=? AND project<>?)`, project, label, label, host, host, host, project).Scan(&taken)
	if err != nil {
		return err
	}
	if taken > 0 {
		return ErrTaken
	}
	res, err := tx.ExecContext(ctx, "UPDATE projects SET subdomain=? WHERE name=?", sub, project)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if _, err = tx.ExecContext(ctx, "UPDATE aliases SET host=? WHERE project=? AND kind=?", host, project, KindProduction); err != nil {
		return err
	}
	return tx.Commit()
}

// SubdomainTaken reports whether a project has renamed its address to name,
// which a new project can't then be called.
func (s *Store) SubdomainTaken(ctx context.Context, name string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM projects WHERE subdomain=?", name).Scan(&n)
	return n > 0, err
}
