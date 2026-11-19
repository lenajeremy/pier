package waitlist

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registered as "sqlite"
)

// Store keeps the waitlist in a single SQLite file, the same way pierd keeps
// its state. Only the address and the time it joined are stored.
type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("create data dir: %w", err)
		}
	}
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // one writer is plenty for a waitlist and avoids SQLITE_BUSY
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS waitlist (
		id         INTEGER PRIMARY KEY,
		email      TEXT NOT NULL UNIQUE,
		created_at TEXT NOT NULL
	)`); err != nil {
		db.Close()
		return nil, fmt.Errorf("create table: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// Add stores a normalized address. It reports false when the address was
// already on the list.
func (s *Store) Add(ctx context.Context, email string) (added bool, err error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO waitlist (email, created_at) VALUES (?, ?) ON CONFLICT(email) DO NOTHING`,
		email, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

type Entry struct {
	Email     string
	CreatedAt string
}

// All returns every entry, oldest first, for exporting.
func (s *Store) All(ctx context.Context) ([]Entry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT email, created_at FROM waitlist ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.Email, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
