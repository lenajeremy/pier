package store

import (
	"context"
	"fmt"
	"pier/internal/names"
	"time"
)

// QueuePreviewCleanup first removes routing eligibility and records a durable
// filesystem job. A restart can finish a partially removed archive or tree.
const disposable = `target='preview' AND status IN ('ready','failed') AND keep_history=0 AND created_at<? AND NOT EXISTS(SELECT 1 FROM aliases WHERE deployment_id=deployments.id) AND NOT EXISTS(SELECT 1 FROM projects WHERE rolled_back_from=deployments.id)`

func (s *Store) CleanupCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM deployments WHERE `+disposable+`)+(SELECT count(*) FROM cleanup_files)`, time.Now().Add(-30*24*time.Hour).UnixMilli()).Scan(&n)
	return n, err
}
func (s *Store) QueuePreviewCleanup(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO cleanup_files(id) SELECT id FROM deployments WHERE `+disposable, time.Now().Add(-30*24*time.Hour).UnixMilli()); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM deployments WHERE id IN (SELECT id FROM cleanup_files)`); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) CleanupFiles(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id FROM cleanup_files")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
func (s *Store) FinishCleanup(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM cleanup_files WHERE id=?", id)
	return err
}

// IDTaken says whether a deployment ID is in use: by a deployment, or by a
// removed one whose files are still waiting to be deleted. The database
// refuses a new deployment with such an ID, so a build must not pick one.
func (s *Store) IDTaken(ctx context.Context, id string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM deployments WHERE id=?)+(SELECT count(*) FROM cleanup_files WHERE id=?)`, id, id).Scan(&n)
	return n > 0, err
}

func (s *Store) SaveAddresses(ctx context.Context, address, base string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for k, v := range map[string]string{SettingAddress: address, SettingBaseDomain: base} {
		if _, err = tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, k, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// AlignAliases moves generated production and branch addresses at startup,
// before accepting builds. Deployment IDs and custom domains stay valid.
func (s *Store) AlignAliases(ctx context.Context, base string) error {
	aliases, err := s.Aliases(ctx)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Production follows the name its owner gave its address, if any.
	labels := map[string]string{}
	rows, err := tx.QueryContext(ctx, "SELECT name, subdomain FROM projects WHERE subdomain<>''")
	if err != nil {
		return err
	}
	for rows.Next() {
		var name, sub string
		if err := rows.Scan(&name, &sub); err != nil {
			rows.Close()
			return err
		}
		labels[name] = sub
	}
	rows.Close()
	for _, a := range aliases {
		label := a.Project
		if sub := labels[a.Project]; sub != "" {
			label = sub
		}
		host := names.Production(label, base)
		if a.Kind == KindBranch {
			host = names.Branch(a.Project, a.Branch, base)
		}
		if host != a.Host {
			if _, err = tx.ExecContext(ctx, "UPDATE aliases SET host=? WHERE host=?", host, a.Host); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
func (s *Store) ChangeOwnerPassword(ctx context.Context, id int64, previous, hash string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, "UPDATE users SET password_hash=?,failed_logins=0,locked_until=0 WHERE id=? AND password_hash=?", hash, id, previous)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("account changed during password update")
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}
