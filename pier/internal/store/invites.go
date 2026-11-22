package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// InviteFor is how long an invite link works.
const InviteFor = 7 * 24 * time.Hour

// Invite is a link the owner made for one person to join with a role. Only
// a hash of its token is kept, so the link itself is shown once, when it's
// made. It works once, and not after it expires.
type Invite struct {
	ID        int64     `json:"id"`
	TokenHash string    `json:"-"`
	Role      string    `json:"role"`
	CreatedBy string    `json:"created_by"` // the inviter's name
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

const inviteCols = `i.id, i.token_hash, i.role, COALESCE((SELECT CASE u.name WHEN '' THEN u.email ELSE u.name END FROM users u WHERE u.id = i.created_by), ''), i.created_at, i.expires_at`

func scanInvite(sc interface{ Scan(...any) error }) (Invite, error) {
	var in Invite
	var created, expires int64
	err := sc.Scan(&in.ID, &in.TokenHash, &in.Role, &in.CreatedBy, &created, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return in, ErrNotFound
	}
	in.CreatedAt, in.ExpiresAt = time.UnixMilli(created), time.UnixMilli(expires)
	return in, err
}

// CreateInvite keeps a new invite's hash and role.
func (s *Store) CreateInvite(ctx context.Context, hash, role string, by int64) (Invite, error) {
	if role != RoleMember && role != RoleViewer {
		return Invite{}, errors.New("invite as a member or a viewer")
	}
	now := time.Now()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO invites(token_hash, role, created_by, created_at, expires_at) VALUES(?, ?, ?, ?, ?)`,
		hash, role, by, now.UnixMilli(), now.Add(InviteFor).UnixMilli())
	if err != nil {
		return Invite{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Invite{}, err
	}
	return scanInvite(s.db.QueryRowContext(ctx, `SELECT `+inviteCols+` FROM invites i WHERE i.id = ?`, id))
}

// Invites lists the invites still waiting to be used, newest first.
func (s *Store) Invites(ctx context.Context) ([]Invite, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+inviteCols+` FROM invites i
		WHERE i.used_at = 0 AND i.expires_at > ? ORDER BY i.created_at DESC, i.id DESC`, time.Now().UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Invite{}
	for rows.Next() {
		in, err := scanInvite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// InviteByHash finds an invite that can still be used.
func (s *Store) InviteByHash(ctx context.Context, hash string) (Invite, error) {
	return scanInvite(s.db.QueryRowContext(ctx, `SELECT `+inviteCols+` FROM invites i
		WHERE i.token_hash = ? AND i.used_at = 0 AND i.expires_at > ?`, hash, time.Now().UnixMilli()))
}

// RevokeInvite stops an unused invite from working.
func (s *Store) RevokeInvite(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM invites WHERE id = ? AND used_at = 0`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AcceptInvite makes the invited person's account, with the invite's role,
// and uses the invite up, in one step: two people opening the same link at
// once can't both get in. ErrNotFound means the invite was used, revoked or
// ran out; ErrExists, that the email already has an account.
func (s *Store) AcceptInvite(ctx context.Context, hash, email, name, passwordHash string) (User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	now := time.Now().UnixMilli()
	var inviteID int64
	var role string
	err = tx.QueryRowContext(ctx, `SELECT id, role FROM invites WHERE token_hash = ? AND used_at = 0 AND expires_at > ?`, hash, now).
		Scan(&inviteID, &role)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	if role != RoleMember && role != RoleViewer {
		return User{}, ErrNotFound
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO users(email, name, password_hash, role, created_at, last_active_at) VALUES(?, ?, ?, ?, ?, ?)`,
		email, name, passwordHash, role, now, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return User{}, ErrExists
		}
		return User{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return User{}, err
	}
	used, err := tx.ExecContext(ctx, `UPDATE invites SET used_at = ?, used_by = ? WHERE id = ? AND used_at = 0`, now, id, inviteID)
	if err != nil {
		return User{}, err
	}
	if n, _ := used.RowsAffected(); n != 1 {
		return User{}, ErrNotFound
	}
	u, err := scanUser(tx.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
	if err != nil {
		return User{}, err
	}
	return u, tx.Commit()
}
