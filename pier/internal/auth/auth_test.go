package auth

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"pier/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "pier.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestSignInLocksAfterFiveWrongPasswords(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	hash, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUser(ctx, "owner@example.com", "Owner", hash); err != nil {
		t.Fatal(err)
	}

	if _, err := SignIn(ctx, st, "OWNER@example.com", "correct horse battery"); err != nil {
		t.Fatalf("right password, any letter case: %v", err)
	}
	for want := Tries - 1; want >= 1; want-- {
		_, err := SignIn(ctx, st, "owner@example.com", "wrong")
		var w WrongError
		if !errors.As(err, &w) || w.Left != want {
			t.Fatalf("want %d tries left, got %v", want, err)
		}
	}
	if _, err := SignIn(ctx, st, "owner@example.com", "wrong"); !errors.Is(err, ErrLocked) {
		t.Fatalf("fifth wrong password should lock, got %v", err)
	}
	if _, err := SignIn(ctx, st, "owner@example.com", "correct horse battery"); !errors.Is(err, ErrLocked) {
		t.Fatalf("the right password mustn't open a locked account, got %v", err)
	}
}

func TestUnknownEmailLooksLikeAWrongPassword(t *testing.T) {
	_, err := SignIn(context.Background(), openStore(t), "nobody@example.com", "whatever")
	var w WrongError
	if !errors.As(err, &w) || w.Left != -1 {
		t.Fatalf("got %v", err)
	}
}

func TestLockRunsOut(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	hash, _ := HashPassword("correct horse battery")
	id, _ := st.CreateUser(ctx, "owner@example.com", "Owner", hash)
	// A lock that ended a moment ago.
	if _, err := st.RecordFailedLogin(ctx, id, 1, -time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := SignIn(ctx, st, "owner@example.com", "correct horse battery"); err != nil {
		t.Fatalf("an expired lock should let the owner in: %v", err)
	}
}

func TestRules(t *testing.T) {
	if CheckPasswordRules("short") == nil {
		t.Error("a 5-character password passed")
	}
	if CheckPasswordRules("twelve chars") != nil {
		t.Error("a 12-character password failed")
	}
	for in, want := range map[string]string{
		"jeremiah@lenajeremy.xyz": "Jeremiah", "ana.lopez@x.io": "Ana", "dev+pier@x.io": "Dev",
	} {
		if got := NameFromEmail(in); got != want {
			t.Errorf("NameFromEmail(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]bool{"a@b.co": true, "a@b": false, "a b@c.io": false, "@c.io": false} {
		if ValidEmail(in) != want {
			t.Errorf("ValidEmail(%q) != %v", in, want)
		}
	}
}
