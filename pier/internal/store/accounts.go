package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Setting keys.
const (
	SettingAddress    = "address"     // where the dashboard and API live: a host, or the bare IP
	SettingBaseDomain = "base_domain" // project addresses go under it; empty until one is chosen
	SettingPublicIP   = "public_ip"   // the address the DNS records point at
)

// Setting returns a setting's value, or "" if it isn't set.
func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetSetting stores a setting.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value)
	return err
}

// User is someone who can sign in. There's one, the owner, until teams come.
type User struct {
	ID           int64     `json:"id"`
	Email        string    `json:"email"`
	Name         string    `json:"name"`
	PasswordHash string    `json:"-"`
	FailedLogins int       `json:"-"`
	LockedUntil  time.Time `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

const userCols = `id, email, name, password_hash, failed_logins, locked_until, created_at`

func scanUser(sc interface{ Scan(...any) error }) (User, error) {
	var u User
	var locked, created int64
	err := sc.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.FailedLogins, &locked, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrNotFound
	}
	if locked > 0 {
		u.LockedUntil = time.UnixMilli(locked)
	}
	u.CreatedAt = time.UnixMilli(created)
	return u, err
}

// CreateUser adds an account and returns its ID.
func (s *Store) CreateUser(ctx context.Context, email, name, passwordHash string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO users(email, name, password_hash, created_at) VALUES(?, ?, ?, ?)`,
		email, name, passwordHash, time.Now().UnixMilli())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UserCount says how many accounts exist; zero means Pier isn't set up yet.
func (s *Store) UserCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// UserByEmail finds an account, ignoring the email's letter case.
func (s *Store) UserByEmail(ctx context.Context, email string) (User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE email = ?`, email))
}

func (s *Store) UserByID(ctx context.Context, id int64) (User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
}

// Owner returns the first account, the one pierd init made.
func (s *Store) Owner(ctx context.Context) (User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users ORDER BY id LIMIT 1`))
}

// RecordFailedLogin counts a wrong password and, from the limit on, locks the
// account until the given time.
func (s *Store) RecordFailedLogin(ctx context.Context, id int64, limit int, lockFor time.Duration) (User, error) {
	_, err := s.db.ExecContext(ctx, `
		UPDATE users SET
			failed_logins = failed_logins + 1,
			locked_until = CASE WHEN failed_logins + 1 >= ? THEN ? ELSE locked_until END
		WHERE id = ?`, limit, time.Now().Add(lockFor).UnixMilli(), id)
	if err != nil {
		return User{}, err
	}
	return s.UserByID(ctx, id)
}

// ClearFailedLogins resets the count after a good password, or once a lock
// has run out.
func (s *Store) ClearFailedLogins(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET failed_logins = 0, locked_until = 0 WHERE id = ?`, id)
	return err
}

// SetPassword changes an account's password and unlocks it.
func (s *Store) SetPassword(ctx context.Context, id int64, passwordHash string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET password_hash = ?, failed_logins = 0, locked_until = 0 WHERE id = ?`, passwordHash, id)
	return err
}

// Session kinds.
const (
	SessionBrowser = "browser"
	SessionCLI     = "cli"
)

// Session is one signed-in browser or computer. Only a hash of its token is
// kept, so a copy of the database can't be used to sign in.
type Session struct {
	TokenHash  string    `json:"-"`
	UserID     int64     `json:"-"`
	Kind       string    `json:"kind"`
	Name       string    `json:"name"`
	IP         string    `json:"ip"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

func (s *Store) CreateSession(ctx context.Context, se Session) error {
	now := time.Now()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions(token_hash, user_id, kind, name, ip, created_at, last_seen_at, expires_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		se.TokenHash, se.UserID, se.Kind, se.Name, se.IP, now.UnixMilli(), now.UnixMilli(), se.ExpiresAt.UnixMilli())
	return err
}

// SessionByHash finds a session that hasn't expired.
func (s *Store) SessionByHash(ctx context.Context, hash string) (Session, error) {
	var se Session
	var created, seen, expires int64
	err := s.db.QueryRowContext(ctx, `
		SELECT token_hash, user_id, kind, name, ip, created_at, last_seen_at, expires_at
		FROM sessions WHERE token_hash = ? AND expires_at > ?`, hash, time.Now().UnixMilli()).
		Scan(&se.TokenHash, &se.UserID, &se.Kind, &se.Name, &se.IP, &created, &seen, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return se, ErrNotFound
	}
	se.CreatedAt, se.LastSeenAt, se.ExpiresAt = time.UnixMilli(created), time.UnixMilli(seen), time.UnixMilli(expires)
	return se, err
}

// TouchSession records that a session was just used, and extends it.
func (s *Store) TouchSession(ctx context.Context, hash, ip string, expires time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET last_seen_at = ?, ip = ?, expires_at = ? WHERE token_hash = ?`,
		time.Now().UnixMilli(), ip, expires.UnixMilli(), hash)
	return err
}

func (s *Store) DeleteSession(ctx context.Context, hash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, hash)
	return err
}

// DeleteSessions signs a user out everywhere, except the session kept (if any).
func (s *Store) DeleteSessions(ctx context.Context, userID int64, keep string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND token_hash != ?`, userID, keep)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteExpiredSessions clears sessions that ran out.
func (s *Store) DeleteExpiredSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, time.Now().UnixMilli())
	return err
}

// Sessions lists a user's sessions, most recently used first.
func (s *Store) Sessions(ctx context.Context, userID int64) ([]Session, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT token_hash, user_id, kind, name, ip, created_at, last_seen_at, expires_at
		FROM sessions WHERE user_id = ? AND expires_at > ? ORDER BY last_seen_at DESC`, userID, time.Now().UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var se Session
		var created, seen, expires int64
		if err := rows.Scan(&se.TokenHash, &se.UserID, &se.Kind, &se.Name, &se.IP, &created, &seen, &expires); err != nil {
			return nil, err
		}
		se.CreatedAt, se.LastSeenAt, se.ExpiresAt = time.UnixMilli(created), time.UnixMilli(seen), time.UnixMilli(expires)
		out = append(out, se)
	}
	return out, rows.Err()
}
