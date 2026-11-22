package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

// Roles. Every account has one, and it holds across the whole server.
const (
	RoleOwner  = "owner"  // the account pierd init made: everything, including members and the server's settings
	RoleMember = "member" // deploys and changes every project
	RoleViewer = "viewer" // looks, and never sees a variable's value
)

// ErrOwner means the change would remove or demote the owner, which nobody can.
var ErrOwner = errors.New("the owner can't be removed or demoted")

// User is someone who can sign in.
type User struct {
	ID           int64     `json:"id"`
	Email        string    `json:"email"`
	Name         string    `json:"name"`
	Role         string    `json:"role"`
	PasswordHash string    `json:"-"`
	FailedLogins int       `json:"-"`
	LockedUntil  time.Time `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
	LastActiveAt time.Time `json:"last_active_at"`
}

// DisplayName is what activity and deployments call the person: their
// name, or their email when they have none.
func (u User) DisplayName() string {
	if u.Name != "" {
		return u.Name
	}
	return u.Email
}

const userCols = `id, email, name, role, password_hash, failed_logins, locked_until, created_at, last_active_at`

func scanUser(sc interface{ Scan(...any) error }) (User, error) {
	var u User
	var locked, created, active int64
	err := sc.Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.PasswordHash, &u.FailedLogins, &locked, &created, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrNotFound
	}
	if locked > 0 {
		u.LockedUntil = time.UnixMilli(locked)
	}
	u.CreatedAt = time.UnixMilli(created)
	if active > 0 {
		u.LastActiveAt = time.UnixMilli(active)
	}
	return u, err
}

// CreateUser adds an account and returns its ID. The first account on a
// server is its owner; anyone else joins with an invite (AcceptInvite),
// which says their role, so an account made here any other way can only look.
func (s *Store) CreateUser(ctx context.Context, email, name, passwordHash string) (int64, error) {
	now := time.Now().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO users(email, name, password_hash, created_at, last_active_at, role)
		VALUES(?, ?, ?, ?, ?, CASE WHEN EXISTS(SELECT 1 FROM users WHERE role = 'owner') THEN 'viewer' ELSE 'owner' END)`,
		email, name, passwordHash, now, now)
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

// Owner returns the owner's account, the one pierd init made.
func (s *Store) Owner(ctx context.Context) (User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE role = 'owner' ORDER BY id LIMIT 1`))
}

// Users lists everyone who can sign in: the owner first, then by name.
func (s *Store) Users(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userCols+` FROM users
		ORDER BY role != 'owner', CASE name WHEN '' THEN email ELSE name END COLLATE NOCASE, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// SetRole makes someone a member or a viewer. The owner stays the owner, and
// nobody else can become one.
func (s *Store) SetRole(ctx context.Context, id int64, role string) (User, error) {
	if role != RoleMember && role != RoleViewer {
		return User{}, fmt.Errorf("unknown role %q", role)
	}
	u, err := s.UserByID(ctx, id)
	if err != nil {
		return u, err
	}
	if u.Role == RoleOwner {
		return u, ErrOwner
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE users SET role = ? WHERE id = ? AND role != 'owner'`, role, id); err != nil {
		return u, err
	}
	u.Role = role
	return u, nil
}

// RemoveUser deletes an account and signs it out everywhere: its browser
// sessions and the tokens its computers use go with it. The owner can't be
// removed.
func (s *Store) RemoveUser(ctx context.Context, id int64) (User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	u, err := scanUser(tx.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
	if err != nil {
		return u, err
	}
	if u.Role == RoleOwner {
		return u, ErrOwner
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, id); err != nil {
		return u, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ? AND role != 'owner'`, id); err != nil {
		return u, err
	}
	return u, tx.Commit()
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
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE users SET last_active_at = ? WHERE id = ?`, now.UnixMilli(), se.UserID)
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

// TouchSession records that a session was just used, and extends it. Its
// account was active then too, which the Members page shows.
func (s *Store) TouchSession(ctx context.Context, hash, ip string, expires time.Time) error {
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET last_seen_at = ?, ip = ?, expires_at = ? WHERE token_hash = ?`,
		now, ip, expires.UnixMilli(), hash)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE users SET last_active_at = ? WHERE id = (SELECT user_id FROM sessions WHERE token_hash = ?)`, now, hash)
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

// DeleteExpiredSessions clears sessions that ran out, and invites that did.
func (s *Store) DeleteExpiredSessions(ctx context.Context) error {
	now := time.Now().UnixMilli()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM invites WHERE expires_at <= ?`, now)
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
