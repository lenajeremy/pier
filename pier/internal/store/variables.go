package store

import (
	"context"
	"fmt"
	"time"
)

// Variable is one environment variable in one scope. Value is sealed: the
// store never sees it in the clear.
type Variable struct {
	Project   string
	Scope     string // TargetProduction or TargetPreview
	Name      string
	Value     string
	UpdatedAt time.Time
}

func changedCol(scope string) (string, error) {
	switch scope {
	case TargetProduction:
		return "vars_changed_production", nil
	case TargetPreview:
		return "vars_changed_preview", nil
	}
	return "", fmt.Errorf("unknown scope %q", scope)
}

// SetVariable adds or changes a variable in one scope.
func (s *Store) SetVariable(ctx context.Context, v Variable) error {
	col, err := changedCol(v.Scope)
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO variables(project, scope, name, value, updated_at) VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(project, scope, name) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		v.Project, v.Scope, v.Name, v.Value, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE projects SET `+col+` = ? WHERE name = ?`, now, v.Project); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteVariable removes a variable from one scope. It reports whether
// there was one.
func (s *Store) DeleteVariable(ctx context.Context, project, scope, name string) (bool, error) {
	col, err := changedCol(scope)
	if err != nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `DELETE FROM variables WHERE project = ? AND scope = ? AND name = ?`, project, scope, name)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE projects SET `+col+` = ? WHERE name = ?`, time.Now().UnixMilli(), project); err != nil {
			return false, err
		}
	}
	return n > 0, tx.Commit()
}

// Variables lists a project's variables, in every scope, by name.
func (s *Store) Variables(ctx context.Context, project string) ([]Variable, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT project, scope, name, value, updated_at FROM variables WHERE project = ? ORDER BY name, scope`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Variable
	for rows.Next() {
		var v Variable
		var updated int64
		if err := rows.Scan(&v.Project, &v.Scope, &v.Name, &v.Value, &updated); err != nil {
			return nil, err
		}
		v.UpdatedAt = time.UnixMilli(updated)
		out = append(out, v)
	}
	return out, rows.Err()
}

// VariablesChanged says when each scope's variables last changed: a set or a
// delete. Zero means never.
func (s *Store) VariablesChanged(ctx context.Context, project string) (production, preview time.Time, err error) {
	var p, v int64
	err = s.db.QueryRowContext(ctx, `SELECT vars_changed_production, vars_changed_preview FROM projects WHERE name = ?`, project).Scan(&p, &v)
	if p > 0 {
		production = time.UnixMilli(p)
	}
	if v > 0 {
		preview = time.UnixMilli(v)
	}
	return
}
