package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Cloudflare settings, kept in settings. The token is sealed.
const (
	SettingCloudflareAccount = "cloudflare_account"
	SettingCloudflareToken   = "cloudflare_token"
	SettingCloudflareTokenID = "cloudflare_token_id"
	SettingCloudflareZone    = "cloudflare_zone"   // the base domain's zone, when it's on Cloudflare
	SettingCloudflarePrefix  = "cloudflare_prefix" // what every bucket this server makes starts with
)

// Bucket is one of a project's buckets: an R2 bucket, the environments it
// serves, the prefix its variables are named with, and the key that
// reaches it alone. Secret is sealed.
type Bucket struct {
	ID           int64     `json:"id"`
	Project      string    `json:"project"`
	Name         string    `json:"name"`
	Bucket       string    `json:"bucket"`
	Public       bool      `json:"public"`
	PublicURL    string    `json:"public_url"`
	Environments []string  `json:"environments"`
	Prefix       string    `json:"prefix"`
	KeyID        string    `json:"-"`
	Secret       string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
	CreatedBy    string    `json:"created_by,omitempty"`
}

// Serves reports whether the bucket is linked to a target.
func (b Bucket) Serves(target string) bool {
	for _, e := range b.Environments {
		if e == target {
			return true
		}
	}
	return false
}

const bucketCols = `id, project, name, bucket, public, public_url, environments, prefix, key_id, secret, created_at, created_by`

func scanBucket(sc interface{ Scan(...any) error }) (Bucket, error) {
	var b Bucket
	var created int64
	var envs string
	err := sc.Scan(&b.ID, &b.Project, &b.Name, &b.Bucket, &b.Public, &b.PublicURL, &envs, &b.Prefix, &b.KeyID, &b.Secret, &created, &b.CreatedBy)
	b.CreatedAt = time.UnixMilli(created)
	b.Environments = splitEnvironments(envs)
	return b, err
}

func splitEnvironments(s string) []string {
	out := []string{}
	for _, e := range strings.Split(s, ",") {
		if e != "" {
			out = append(out, e)
		}
	}
	return out
}

// AddBucket records a project's bucket and returns its ID.
func (s *Store) AddBucket(ctx context.Context, b Bucket) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO storage_buckets(project, name, bucket, public, public_url, environments, prefix, key_id, secret, created_at, created_by)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		b.Project, b.Name, b.Bucket, b.Public, b.PublicURL, strings.Join(b.Environments, ","), b.Prefix, b.KeyID, b.Secret, b.CreatedAt.UnixMilli(), b.CreatedBy)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// BucketNamed is a project's bucket by its name, or ErrNotFound.
func (s *Store) BucketNamed(ctx context.Context, project, name string) (Bucket, error) {
	b, err := scanBucket(s.db.QueryRowContext(ctx, `SELECT `+bucketCols+` FROM storage_buckets WHERE project = ? AND name = ?`, project, name))
	if errors.Is(err, sql.ErrNoRows) {
		return b, ErrNotFound
	}
	return b, err
}

// Buckets lists a project's buckets, oldest first, or every project's
// with project empty.
func (s *Store) Buckets(ctx context.Context, project string) ([]Bucket, error) {
	q, args := `SELECT `+bucketCols+` FROM storage_buckets ORDER BY project, id`, []any{}
	if project != "" {
		q, args = `SELECT `+bucketCols+` FROM storage_buckets WHERE project = ? ORDER BY id`, []any{project}
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Bucket{}
	for rows.Next() {
		b, err := scanBucket(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// LinkBucket sets which environments a bucket serves and its prefix.
func (s *Store) LinkBucket(ctx context.Context, project, name string, environments []string, prefix string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE storage_buckets SET environments = ?, prefix = ? WHERE project = ? AND name = ?`,
		strings.Join(environments, ","), prefix, project, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteBucket forgets one of a project's buckets.
func (s *Store) DeleteBucket(ctx context.Context, project, name string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM storage_buckets WHERE project = ? AND name = ?`, project, name)
	return err
}
