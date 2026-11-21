package store

import (
	"context"
	"strings"
	"time"
)

type Event struct {
	ID         int64  `json:"id"`
	At         int64  `json:"at"`
	Project    string `json:"project"`
	Title      string `json:"title"`
	Detail     string `json:"detail"`
	Deployment string `json:"deployment,omitempty"`
}

// KeepEvents is how long activity is kept.
const KeepEvents = 365 * 24 * time.Hour

// eventsQuery is the page of activity that follows the event with the ID
// before (0 for the newest page), for one project or, with project empty,
// for all. It names only the conditions in use, so SQLite can read the page
// off an index in order and stop: activity_by_project when a project is
// chosen, activity_time when not. One query that covers both, with "no
// project chosen OR project=?", hides the choice from the planner, which
// then walks all activity looking for the project's rows.
func eventsQuery(project string, before int64) (string, []any) {
	var where []string
	var args []any
	if project != "" {
		where = append(where, "project=?")
		args = append(args, project)
	}
	if before != 0 {
		where = append(where, "(at,id)<(SELECT at,id FROM activity WHERE id=?)")
		args = append(args, before)
	}
	q := `SELECT id,at,project,title,detail,CASE WHEN EXISTS(SELECT 1 FROM deployments d WHERE d.id=activity.deployment AND d.project=activity.project) THEN deployment ELSE '' END FROM activity`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	return q + " ORDER BY at DESC,id DESC LIMIT 100", args
}

func (s *Store) Events(ctx context.Context, project string, before int64) ([]Event, error) {
	q, args := eventsQuery(project, before)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.At, &e.Project, &e.Title, &e.Detail, &e.Deployment); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *Store) RecordEvent(ctx context.Context, project, title, detail, id string) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO activity(at,project,title,detail,deployment) VALUES(?,?,?,?,?)", time.Now().UnixMilli(), project, title, detail, id)
	return err
}

// PruneEvents forgets activity older than KeepEvents, and says how many
// events that was. Every sign-in, build and change adds a row, and nothing
// else ever removes one.
func (s *Store) PruneEvents(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM activity WHERE at<?", time.Now().Add(-KeepEvents).UnixMilli())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
