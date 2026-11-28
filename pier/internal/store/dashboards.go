package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Dashboard is a project's own page of panels in Analytics. Spec is the
// panels, variables and settings as JSON; the dashboard reads it, pierd
// only keeps it.
type Dashboard struct {
	ID        int64     `json:"id"`
	Project   string    `json:"-"`
	Name      string    `json:"name"`
	Spec      string    `json:"-"`
	UpdatedAt time.Time `json:"updated_at"`
	UpdatedBy string    `json:"updated_by,omitempty"`
}

// ErrNoDashboard means the project has no dashboard by that ID.
var ErrNoDashboard = errors.New("there's no dashboard by that ID")

// ErrDashboardName means the project already has a dashboard by that name.
var ErrDashboardName = errors.New("there's already a dashboard by that name")

func scanDashboard(row interface{ Scan(...any) error }) (Dashboard, error) {
	var d Dashboard
	var at int64
	err := row.Scan(&d.ID, &d.Project, &d.Name, &d.Spec, &at, &d.UpdatedBy)
	d.UpdatedAt = time.UnixMilli(at)
	return d, err
}

// Dashboards lists a project's dashboards by name.
func (s *Store) Dashboards(ctx context.Context, project string) ([]Dashboard, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, project, name, spec, updated_at, updated_by FROM dashboards WHERE project = ? ORDER BY name COLLATE NOCASE`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Dashboard{}
	for rows.Next() {
		d, err := scanDashboard(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DashboardByID reads one of a project's dashboards.
func (s *Store) DashboardByID(ctx context.Context, project string, id int64) (Dashboard, error) {
	d, err := scanDashboard(s.db.QueryRowContext(ctx, `SELECT id, project, name, spec, updated_at, updated_by FROM dashboards WHERE project = ? AND id = ?`, project, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Dashboard{}, ErrNoDashboard
	}
	return d, err
}

// SaveDashboard creates a dashboard when d.ID is 0, or replaces one.
func (s *Store) SaveDashboard(ctx context.Context, d Dashboard) (Dashboard, error) {
	d.UpdatedAt = time.Now()
	var err error
	if d.ID == 0 {
		var res sql.Result
		res, err = s.db.ExecContext(ctx, `INSERT INTO dashboards(project, name, spec, updated_at, updated_by) VALUES(?, ?, ?, ?, ?)`, d.Project, d.Name, d.Spec, d.UpdatedAt.UnixMilli(), d.UpdatedBy)
		if err == nil {
			d.ID, err = res.LastInsertId()
		}
	} else {
		var res sql.Result
		res, err = s.db.ExecContext(ctx, `UPDATE dashboards SET name = ?, spec = ?, updated_at = ?, updated_by = ? WHERE project = ? AND id = ?`, d.Name, d.Spec, d.UpdatedAt.UnixMilli(), d.UpdatedBy, d.Project, d.ID)
		if err == nil {
			if n, _ := res.RowsAffected(); n == 0 {
				return Dashboard{}, ErrNoDashboard
			}
		}
	}
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return Dashboard{}, ErrDashboardName
	}
	return d, err
}

// DeleteDashboard removes one of a project's dashboards.
func (s *Store) DeleteDashboard(ctx context.Context, project string, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM dashboards WHERE project = ? AND id = ?`, project, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoDashboard
	}
	return nil
}
