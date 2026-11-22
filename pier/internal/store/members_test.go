package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// A server set up before teams has one account. Upgrading makes it the
// owner, and keeps when it was last used.
func TestUpgradeMakesTheOnlyAccountTheOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pier.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 15; i++ {
		if _, err := db.Exec(migrations[i]); err != nil {
			t.Fatalf("migration %d: %v", i+1, err)
		}
	}
	seen := time.Now().Add(-time.Hour).UnixMilli()
	if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = 15;
		INSERT INTO users(id,email,name,password_hash,created_at) VALUES(1,'owner@example.com','Owner','x',1);
		INSERT INTO sessions(token_hash,user_id,kind,created_at,last_seen_at,expires_at) VALUES('h',1,'browser',1,%d,%d);`,
		seen, time.Now().Add(time.Hour).UnixMilli())); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	o, err := s.Owner(context.Background())
	if err != nil || o.Email != "owner@example.com" || o.Role != RoleOwner || o.LastActiveAt.UnixMilli() != seen {
		t.Fatalf("after upgrading, the owner is %+v (%v)", o, err)
	}
}

func TestRolesAndRemoval(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	owner, _ := s.CreateUser(ctx, "owner@example.com", "Owner", "x")
	other, _ := s.CreateUser(ctx, "ada@example.com", "Ada", "x")
	if u, _ := s.UserByID(ctx, owner); u.Role != RoleOwner {
		t.Fatalf("the first account is %q", u.Role)
	}
	if u, _ := s.UserByID(ctx, other); u.Role != RoleViewer {
		t.Fatalf("a second account made without an invite is %q, want viewer", u.Role)
	}
	if _, err := s.SetRole(ctx, owner, RoleViewer); !errors.Is(err, ErrOwner) {
		t.Fatalf("demoting the owner: %v", err)
	}
	if _, err := s.SetRole(ctx, other, RoleOwner); err == nil {
		t.Fatal("made a second owner")
	}
	if u, err := s.SetRole(ctx, other, RoleMember); err != nil || u.Role != RoleMember {
		t.Fatalf("making Ada a member: %+v %v", u, err)
	}
	if _, err := s.RemoveUser(ctx, owner); !errors.Is(err, ErrOwner) {
		t.Fatalf("removing the owner: %v", err)
	}
	if err := s.CreateSession(ctx, Session{TokenHash: "ada", UserID: other, Kind: SessionCLI, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RemoveUser(ctx, other); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionByHash(ctx, "ada"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Ada's token still works after removal: %v", err)
	}
	if us, _ := s.Users(ctx); len(us) != 1 || us[0].ID != owner {
		t.Fatalf("left: %+v", us)
	}
}

func TestInvitesWorkOnceAndRunOut(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	owner, _ := s.CreateUser(ctx, "owner@example.com", "Owner", "x")
	in, err := s.CreateInvite(ctx, "h1", RoleMember, owner)
	if err != nil || in.CreatedBy != "Owner" || in.ExpiresAt.Sub(in.CreatedAt) != InviteFor {
		t.Fatalf("made %+v (%v)", in, err)
	}
	if _, err := s.CreateInvite(ctx, "h0", RoleOwner, owner); err == nil {
		t.Fatal("made an invite to be a second owner")
	}
	u, err := s.AcceptInvite(ctx, "h1", "ada@example.com", "Ada", "x")
	if err != nil || u.Role != RoleMember {
		t.Fatalf("accepting: %+v %v", u, err)
	}
	if _, err := s.AcceptInvite(ctx, "h1", "grace@example.com", "Grace", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("accepting twice: %v", err)
	}
	if _, err := s.CreateInvite(ctx, "h2", RoleViewer, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcceptInvite(ctx, "h2", "ADA@example.com", "Ada again", "x"); !errors.Is(err, ErrExists) {
		t.Fatalf("an email that has an account: %v", err)
	}
	if _, err := s.InviteByHash(ctx, "h2"); err != nil {
		t.Fatalf("a refused acceptance used the invite up: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE invites SET expires_at = ? WHERE token_hash = 'h2'`, time.Now().Add(-time.Second).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InviteByHash(ctx, "h2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an expired invite: %v", err)
	}
	if _, err := s.AcceptInvite(ctx, "h2", "grace@example.com", "Grace", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("accepting an expired invite: %v", err)
	}
	if list, _ := s.Invites(ctx); len(list) != 0 {
		t.Fatalf("still waiting: %+v", list)
	}
}
