package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type SharedVariable struct {
	Name       string   `json:"name"`
	Value      string   `json:"-"`
	Production bool     `json:"production"`
	Preview    bool     `json:"preview"`
	Projects   []string `json:"projects"`
}

func (s *Store) SharedVariables(ctx context.Context) ([]SharedVariable, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT v.name,v.production,v.preview,l.project FROM shared_variables v LEFT JOIN shared_links l ON l.name=v.name ORDER BY v.name,l.project`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SharedVariable{}
	for rows.Next() {
		var v SharedVariable
		var project sql.NullString
		if err := rows.Scan(&v.Name, &v.Production, &v.Preview, &project); err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].Name != v.Name {
			v.Projects = []string{}
			out = append(out, v)
		}
		if project.Valid {
			i := len(out) - 1
			out[i].Projects = append(out[i].Projects, project.String)
		}
	}
	return out, rows.Err()
}

type inheritance struct{ project, scope string }

func sharedUsers(ctx context.Context, tx *sql.Tx, name string) (map[inheritance]bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT l.project,s.scope FROM shared_links l JOIN shared_variables v USING(name)
 CROSS JOIN (SELECT 'production' AS scope UNION ALL SELECT 'preview') s
 WHERE v.name=? AND CASE s.scope WHEN 'production' THEN v.production ELSE v.preview END
 AND NOT EXISTS(SELECT 1 FROM variables p WHERE p.name=v.name AND p.project=l.project AND p.scope=s.scope)`, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[inheritance]bool{}
	for rows.Next() {
		var k inheritance
		if err := rows.Scan(&k.project, &k.scope); err != nil {
			return nil, err
		}
		out[k] = true
	}
	return out, rows.Err()
}

func (s *Store) SaveShared(ctx context.Context, v SharedVariable, create, remove bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UnixMilli()
	before, err := sharedUsers(ctx, tx, v.Name)
	if err != nil {
		return err
	}
	var result sql.Result
	switch {
	case remove:
		result, err = tx.ExecContext(ctx, "DELETE FROM shared_variables WHERE name=?", v.Name)
	case create:
		result, err = tx.ExecContext(ctx, `INSERT INTO shared_variables(name,value,production,preview,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(name) DO NOTHING`, v.Name, v.Value, v.Production, v.Preview, now)
	default:
		result, err = tx.ExecContext(ctx, `UPDATE shared_variables SET value=coalesce(nullif(?,''),value),production=?,preview=?,updated_at=? WHERE name=?`, v.Value, v.Production, v.Preview, now, v.Name)
	}
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		if create {
			return ErrExists
		}
		return ErrNotFound
	}
	if !remove {
		if _, err = tx.ExecContext(ctx, "DELETE FROM shared_links WHERE name=?", v.Name); err != nil {
			return err
		}
		for _, project := range v.Projects {
			result, err = tx.ExecContext(ctx, `INSERT INTO shared_links(name,project) SELECT ?,name FROM projects WHERE name=?`, v.Name, project)
			if err != nil {
				return err
			}
			if n, _ := result.RowsAffected(); n == 0 {
				return errors.New("linked project no longer exists")
			}
		}
	}
	after, err := sharedUsers(ctx, tx, v.Name)
	if err != nil {
		return err
	}
	// Removed links and new or replaced inherited values affect only their own build scopes.
	for k := range before {
		if !after[k] {
			after[k] = false
		}
	}
	for k, linked := range after {
		if linked == before[k] && v.Value == "" {
			continue
		}
		col, _ := changedCol(k.scope)
		if _, err = tx.ExecContext(ctx, "UPDATE projects SET "+col+"=? WHERE name=?", now, k.project); err != nil {
			return err
		}
	}
	action := "Updated shared variable"
	if create {
		action = "Added shared variable"
	}
	if remove {
		action = "Removed shared variable"
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO activity(at,title,detail) VALUES(?,?,?)", now, action, v.Name); err != nil {
		return err
	}
	return tx.Commit()
}

// BuildVariables returns one effective value per name. An empty Project denotes
// a shared value, whose associated encryption data is independent of the project.
func (s *Store) BuildVariables(ctx context.Context, project, scope string) ([]Variable, error) {
	if _, err := changedCol(scope); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT project,name,value FROM variables WHERE project=? AND scope=?
 UNION ALL SELECT '',v.name,v.value FROM shared_variables v JOIN shared_links l USING(name)
 WHERE l.project=? AND v.`+scope+`=1 AND NOT EXISTS (SELECT 1 FROM variables p WHERE p.name=v.name AND p.scope=? AND p.project=l.project) ORDER BY name`, project, scope, project, scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var vars []Variable
	for rows.Next() {
		var v Variable
		v.Scope = scope
		if err := rows.Scan(&v.Project, &v.Name, &v.Value); err != nil {
			return nil, err
		}
		vars = append(vars, v)
	}
	return vars, rows.Err()
}
