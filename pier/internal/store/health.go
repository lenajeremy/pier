package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Health states of production's server, as its health checks see it.
const (
	Healthy    = "healthy"    // its last check passed
	Failing    = "failing"    // a check failed, but not enough in a row to act on
	Unhealthy  = "unhealthy"  // enough failed in a row: Pier restarts it and alerts
	Restarting = "restarting" // Pier is restarting it after failed checks
)

// Health is how a production server did at its health checks.
type Health struct {
	Deployment    string    `json:"deployment"`
	Project       string    `json:"project"`
	State         string    `json:"state"`
	Asked         string    `json:"asked"`    // what's checked: "GET /health", or "the process"
	Failures      int       `json:"failures"` // failed checks in a row
	CheckedAt     time.Time `json:"checked_at,omitzero"`
	Since         time.Time `json:"since,omitzero"` // when the state began
	LastFailure   string    `json:"last_failure,omitempty"`
	LastFailureAt time.Time `json:"last_failure_at,omitzero"`
	Restarts      int       `json:"restarts"` // how many times Pier restarted it for failing
	RestartedAt   time.Time `json:"restarted_at,omitzero"`
	NextRestart   time.Time `json:"next_restart,omitzero"` // the soonest Pier restarts it again
}

const healthCols = `deployment, project, state, asked, failures, checked_at, since, last_failure, last_failure_at, restarts, restarted_at, next_restart`

// SaveHealth keeps a server's health.
func (s *Store) SaveHealth(ctx context.Context, h Health) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO health(`+healthCols+`) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(deployment) DO UPDATE SET state = excluded.state, asked = excluded.asked, failures = excluded.failures,
			checked_at = excluded.checked_at, since = excluded.since, last_failure = excluded.last_failure,
			last_failure_at = excluded.last_failure_at, restarts = excluded.restarts, restarted_at = excluded.restarted_at,
			next_restart = excluded.next_restart`,
		h.Deployment, h.Project, h.State, h.Asked, h.Failures, ms(h.CheckedAt), ms(h.Since), h.LastFailure,
		ms(h.LastFailureAt), h.Restarts, ms(h.RestartedAt), ms(h.NextRestart))
	return err
}

// HealthOf returns a deployment's health, and whether it has been checked.
func (s *Store) HealthOf(ctx context.Context, deployment string) (Health, bool, error) {
	var h Health
	var checked, since, failedAt, restarted, next int64
	err := s.db.QueryRowContext(ctx, `SELECT `+healthCols+` FROM health WHERE deployment = ?`, deployment).Scan(
		&h.Deployment, &h.Project, &h.State, &h.Asked, &h.Failures, &checked, &since, &h.LastFailure, &failedAt, &h.Restarts, &restarted, &next)
	if errors.Is(err, sql.ErrNoRows) {
		return h, false, nil
	}
	if err != nil {
		return h, false, err
	}
	h.CheckedAt, h.Since, h.LastFailureAt, h.RestartedAt, h.NextRestart = fromMS(checked), fromMS(since), fromMS(failedAt), fromMS(restarted), fromMS(next)
	return h, true, nil
}

// PruneHealth forgets the health of every deployment but the ones kept:
// what production served before isn't checked any more.
func (s *Store) PruneHealth(ctx context.Context, keep []string) error {
	q := `DELETE FROM health`
	args := make([]any, len(keep))
	if len(keep) > 0 {
		q += ` WHERE deployment NOT IN (?` + strings.Repeat(",?", len(keep)-1) + `)`
		for i, id := range keep {
			args[i] = id
		}
	}
	_, err := s.db.ExecContext(ctx, q, args...)
	return err
}

// ProductionApp is a server production serves, and how its project wants
// it checked.
type ProductionApp struct {
	Project    string
	Deployment string
	Name       string // its container
	Host       string // production's address
	HealthPath string
	HealthOff  bool
}

// ProductionApps lists the servers production serves, one per project
// whose production is a server app.
func (s *Store) ProductionApps(ctx context.Context) ([]ProductionApp, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT a.project, d.id, r.name, a.host, p.health_path, p.health_off FROM aliases a
		JOIN deployments d ON d.id = a.deployment_id
		JOIN app_resources r ON r.deployment = d.id
		JOIN projects p ON p.name = a.project
		WHERE a.kind = ? AND d.status = ? ORDER BY a.project`, KindProduction, Ready)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProductionApp
	for rows.Next() {
		var a ProductionApp
		if err := rows.Scan(&a.Project, &a.Deployment, &a.Name, &a.Host, &a.HealthPath, &a.HealthOff); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
