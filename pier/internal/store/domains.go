package store

import (
	"context"
	"time"
)

type CustomDomain struct {
	ID             int64  `json:"-"`
	Project        string `json:"-"`
	Host           string `json:"host"`
	Redirect       string `json:"redirect,omitempty"`
	Verified       bool   `json:"verified"`
	Checked        int64  `json:"checked"`
	AddressResult  string `json:"address_result"`
	RedirectResult string `json:"redirect_result"`
}

// AddDomain claims both names atomically. Neither may already belong to another project.
func (s *Store) AddDomain(ctx context.Context, project, host, redirect string) error {
	res, err := s.db.ExecContext(ctx, `INSERT INTO custom_domains(project,host,redirect)
		SELECT ?,?,? WHERE NOT EXISTS (
			SELECT 1 FROM custom_domains WHERE host IN (?,?) OR (redirect != '' AND redirect IN (?,?))
		)`, project, host, redirect, host, redirect, host, redirect)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrExists
	}
	return nil
}

func (s *Store) Domains(ctx context.Context, project string) ([]CustomDomain, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,project,host,redirect,verified,checked,address_result,redirect_result
		FROM custom_domains WHERE ?='' OR project=? ORDER BY id`, project, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ds []CustomDomain
	for rows.Next() {
		var d CustomDomain
		if err := rows.Scan(&d.ID, &d.Project, &d.Host, &d.Redirect, &d.Verified, &d.Checked, &d.AddressResult, &d.RedirectResult); err != nil {
			return nil, err
		}
		ds = append(ds, d)
	}
	return ds, rows.Err()
}

// RecordDomainCheck discards an in-flight DNS result if the domain has since
// been removed, re-added, or checked by another request.
func (s *Store) RecordDomainCheck(ctx context.Context, d CustomDomain) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE custom_domains SET verified=?,checked=max(checked+1,?),address_result=?,redirect_result=?
		WHERE id=? AND checked=?`, d.Verified, time.Now().UnixMilli(), d.AddressResult, d.RedirectResult, d.ID, d.Checked)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n != 0, err
}

func (s *Store) RemoveDomain(ctx context.Context, project, host string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM custom_domains WHERE project=? AND host=?`, project, host)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
