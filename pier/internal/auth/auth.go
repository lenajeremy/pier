// Package auth signs the owner in: passwords, session tokens, and the lock
// that stops a password being guessed.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"

	"pier/internal/store"
)

const (
	// MinPassword is the shortest password Pier accepts.
	MinPassword = 12
	// Tries is how many wrong passwords in a row lock the account.
	Tries = 5
	// LockFor is how long the account stays locked.
	LockFor = time.Minute
	// SessionFor is how long a session lasts without being used.
	SessionFor = 30 * 24 * time.Hour
)

// CheckPasswordRules says what's wrong with a new password, if anything.
func CheckPasswordRules(pw string) error {
	if utf8.RuneCountInString(pw) < MinPassword {
		return fmt.Errorf("use %d characters or more", MinPassword)
	}
	if len(pw) > 72 { // bcrypt only reads the first 72 bytes
		return errors.New("use 72 characters or fewer")
	}
	return nil
}

// HashPassword hashes a password for storing.
func HashPassword(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), 12)
	return string(h), err
}

// NewToken returns a random session token and the hash that's stored for it.
func NewToken() (token, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	token = "pier_" + base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token), nil
}

// HashToken is how a token is looked up: only its hash is ever stored.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// NameFromEmail makes a first name to greet the owner with until they set
// one: jeremiah@lenajeremy.xyz becomes Jeremiah.
func NameFromEmail(email string) string {
	local, _, _ := strings.Cut(email, "@")
	local, _, _ = strings.Cut(local, "+")
	for _, sep := range []string{".", "_", "-"} {
		local, _, _ = strings.Cut(local, sep)
	}
	if local == "" {
		return ""
	}
	r, size := utf8.DecodeRuneInString(local)
	return strings.ToUpper(string(r)) + local[size:]
}

// ValidEmail is a light check: something@something.something.
func ValidEmail(email string) bool {
	local, domain, ok := strings.Cut(email, "@")
	return ok && local != "" && strings.Contains(domain, ".") && !strings.ContainsAny(email, " \t\r\n") &&
		!strings.HasPrefix(domain, ".") && !strings.HasSuffix(domain, ".")
}

// Sign-in failures. The messages are shown to the person signing in.
var (
	ErrWrongPassword = errors.New("wrong email or password")
	ErrLocked        = errors.New("locked")
)

// LockedError says how long until the account opens again.
type LockedError struct{ Until time.Time }

func (e LockedError) Error() string   { return "too many wrong passwords" }
func (e LockedError) Is(t error) bool { return t == ErrLocked }

// WrongError says how many tries are left before the account locks.
type WrongError struct{ Left int }

func (e WrongError) Error() string   { return ErrWrongPassword.Error() }
func (e WrongError) Is(t error) bool { return t == ErrWrongPassword }

// SignIn checks an email and password. Every wrong password counts against
// the account, and the fifth in a row locks it for a minute. An unknown email
// is answered the same way as a wrong password, after the same bcrypt work,
// so it doesn't reveal which emails exist.
func SignIn(ctx context.Context, st *store.Store, email, password string) (store.User, error) {
	u, err := st.UserByEmail(ctx, strings.TrimSpace(email))
	if errors.Is(err, store.ErrNotFound) {
		bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return store.User{}, WrongError{Left: -1}
	}
	if err != nil {
		return store.User{}, err
	}
	now := time.Now()
	if u.LockedUntil.After(now) {
		return store.User{}, LockedError{Until: u.LockedUntil}
	}
	if !u.LockedUntil.IsZero() { // a lock ran out: start counting again
		if err := st.ClearFailedLogins(ctx, u.ID); err != nil {
			return store.User{}, err
		}
		u.FailedLogins = 0
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		u, err := st.RecordFailedLogin(ctx, u.ID, Tries, LockFor)
		if err != nil {
			return store.User{}, err
		}
		if u.LockedUntil.After(now) {
			return store.User{}, LockedError{Until: u.LockedUntil}
		}
		return store.User{}, WrongError{Left: Tries - u.FailedLogins}
	}
	if u.FailedLogins > 0 {
		if err := st.ClearFailedLogins(ctx, u.ID); err != nil {
			return store.User{}, err
		}
	}
	return u, nil
}

// dummyHash makes a sign-in with an unknown email cost the same as a real one.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("pier-not-a-real-password"), 12)
