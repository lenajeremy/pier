package store

import (
	"context"
	"database/sql"
	"errors"
)

// GitHubLink returns the repository a project deploys from and the
// installation that reads it. ErrNotFound means the project doesn't deploy
// from GitHub.
func (s *Store) GitHubLink(ctx context.Context, project string) (repoID, installation int64, err error) {
	err = s.db.QueryRowContext(ctx, "SELECT repo_id,installation FROM github_repos WHERE project=?", project).Scan(&repoID, &installation)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return repoID, installation, err
}

// ImportJob reports whether a job is an imported project's first build,
// rather than one GitHub delivered.
func (s *Store) ImportJob(ctx context.Context, id int64) (bool, error) {
	var imported bool
	err := s.db.QueryRowContext(ctx, "SELECT delivery=? FROM github_jobs WHERE id=?", importDelivery, id).Scan(&imported)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return imported, err
}
