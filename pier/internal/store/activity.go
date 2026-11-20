package store

import (
	"context"
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

func (s *Store) Events(ctx context.Context, project string, before int64) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,at,project,title,detail,CASE WHEN EXISTS(SELECT 1 FROM deployments d WHERE d.id=activity.deployment AND d.project=activity.project) THEN deployment ELSE '' END FROM activity WHERE (?='' OR project=?) AND (?=0 OR (at,id)<(SELECT at,id FROM activity WHERE id=?)) ORDER BY at DESC,id DESC LIMIT 100`, project, project, before, before)
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
