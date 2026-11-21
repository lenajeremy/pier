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
	return s.appNames(ctx, `SELECT r.name FROM app_resources r LEFT JOIN deployments d ON d.id=r.deployment WHERE d.id IS NULL OR d.status='failed'`)
}

// idleApp is true for a built deployment d that nothing needs running. A
// deployment is needed while an address points at it (custom domains follow
// production's), while it is where a rollback came from or where the next one
// would go, and while it is the newest of its branch and target: pier
// deploy's previews have only their own address, and a production build held
// back by a rollback waits to be promoted. The rollback target is chosen as
// the API chooses it: the newest production deployment that built, older
// than the one production serves.
const idleApp = `d.status='ready'
	AND NOT EXISTS(SELECT 1 FROM aliases a WHERE a.deployment_id=d.id)
	AND NOT EXISTS(SELECT 1 FROM projects p WHERE p.rolled_back_from=d.id)
	AND EXISTS(SELECT 1 FROM deployments n WHERE n.project=d.project AND n.branch=d.branch AND n.target=d.target
		AND n.status='ready' AND (n.created_at>d.created_at OR (n.created_at=d.created_at AND n.id>d.id)))
	AND d.id IS NOT (SELECT t.id FROM aliases a JOIN deployments cur ON cur.id=a.deployment_id JOIN deployments t ON t.project=a.project
		WHERE a.kind='production' AND a.project=d.project AND t.id!=cur.id AND t.target='production' AND t.status='ready'
		AND t.created_at<cur.created_at ORDER BY t.created_at DESC, t.id LIMIT 1)`

// IdleApps lists the containers of superseded deployments, which can be
// stopped: in one project or, with project empty, in all of them. Their
// images stay, so a rollback or a promote can start one again.
func (s *Store) IdleApps(ctx context.Context, project string) ([]string, error) {
	return s.appNames(ctx, `SELECT r.name FROM app_resources r JOIN deployments d ON d.id=r.deployment
		WHERE (?='' OR d.project=?) AND `+idleApp+` ORDER BY r.name`, project, project)
}

func (s *Store) appNames(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
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
