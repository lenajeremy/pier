package store

import "context"

// Record ownership before invoking Docker. The record outlives an expired
// deployment until both its container and tagged image are gone.
func (s *Store) TrackApp(ctx context.Context, id, name string) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO app_resources(deployment,name) VALUES(?,?)", id, name)
	return err
}
func (s *Store) ForgetApp(ctx context.Context, name string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM app_resources WHERE name=?", name)
	return err
}
func (s *Store) InterruptAppBuilds(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE deployments SET status='failed',error='Pier restarted during application deployment' WHERE status='building' AND id IN (SELECT deployment FROM app_resources)`)
	return err
}
func (s *Store) UnusedApps(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT r.name FROM app_resources r LEFT JOIN deployments d ON d.id=r.deployment WHERE d.id IS NULL OR d.status='failed'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}
