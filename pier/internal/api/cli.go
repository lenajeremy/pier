package api

import (
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"pier/internal/auth"
	"pier/internal/store"
)

// pier login asks pierd for a code, shows it, and opens the dashboard's
// approve page. Whoever is signed in there sees the computer's name and the
// same code and approves it; the CLI, asking every two seconds, then gets a
// token of its own, which acts as that person, with their role. Requests
// live only in memory and expire in 10 minutes.

const (
	loginTTL     = 10 * time.Minute
	maxWaiting   = 50
	codeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789" // no 0/O, 1/I/L
)

type loginRequest struct {
	Code     string
	Name     string // the computer's name, as the CLI reports it
	IP       string
	Created  time.Time
	PollHash string
	Status   string // pending, approved, denied
	Token    string // handed to the CLI once, after approval
	Email    string // whose account the computer signs in to: whoever approved it
}

type logins struct {
	mu sync.Mutex
	m  map[string]*loginRequest // by code
}

func (l *logins) sweep() {
	for code, r := range l.m {
		if time.Since(r.Created) > loginTTL {
			delete(l.m, code)
		}
	}
}

func newCode() string {
	b := make([]byte, 8)
	rand.Read(b)
	out := make([]byte, 0, 9)
	for i, c := range b {
		if i == 4 {
			out = append(out, '-')
		}
		out = append(out, codeAlphabet[int(c)%len(codeAlphabet)])
	}
	return string(out)
}

// normalizeCode accepts "bxrt4kq9" or "BXRT-4KQ9".
func normalizeCode(s string) string {
	s = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(s), "-", ""))
	if len(s) != 8 {
		return ""
	}
	return s[:4] + "-" + s[4:]
}

// cliStart begins a pier login: no account needed, since all it can do is
// put a request in front of someone signed in.
func (a *API) cliStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req)
	name := clip(req.Name, 80)
	if name == "" {
		name = "a computer"
	}
	poll, pollHash, err := auth.NewToken()
	if err != nil {
		a.fail(w, err)
		return
	}
	a.logins.mu.Lock()
	if a.logins.m == nil {
		a.logins.m = map[string]*loginRequest{}
	}
	a.logins.sweep()
	if len(a.logins.m) >= maxWaiting {
		a.logins.mu.Unlock()
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many sign-ins are waiting; try again in a few minutes"})
		return
	}
	code := newCode()
	for a.logins.m[code] != nil {
		code = newCode()
	}
	a.logins.m[code] = &loginRequest{Code: code, Name: name, IP: clientIP(r), Created: time.Now(), PollHash: pollHash, Status: "pending"}
	a.logins.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"code": code, "poll": poll, "approve_path": "/approve?code=" + code, "expires_in": int(loginTTL.Seconds()),
	})
}

// cliPoll is the CLI asking whether its request was answered.
func (a *API) cliPoll(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Poll string `json:"poll"`
	}
	json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req)
	hash := auth.HashToken(req.Poll)
	a.logins.mu.Lock()
	defer a.logins.mu.Unlock()
	for code, lr := range a.logins.m {
		if lr.PollHash != hash {
			continue
		}
		if time.Since(lr.Created) > loginTTL {
			delete(a.logins.m, code)
			break
		}
		switch lr.Status {
		case "approved":
			delete(a.logins.m, code)
			writeJSON(w, http.StatusOK, map[string]any{"status": "approved", "token": lr.Token, "email": lr.Email})
		case "denied":
			delete(a.logins.m, code)
			writeJSON(w, http.StatusOK, map[string]any{"status": "denied"})
		default:
			writeJSON(w, http.StatusOK, map[string]any{"status": "pending"})
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "expired"})
}

// cliRequest is what the approve page shows: which computer, and its code.
func (a *API) cliRequest(w http.ResponseWriter, r *http.Request) {
	code := normalizeCode(r.PathValue("code"))
	a.logins.mu.Lock()
	defer a.logins.mu.Unlock()
	a.logins.sweep()
	lr := a.logins.m[code]
	if lr == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "That code isn't waiting any more. Run pier login again."})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"code": lr.Code, "name": lr.Name, "ip": lr.IP, "created_at": lr.Created, "status": lr.Status,
		"expires_at": lr.Created.Add(loginTTL),
	})
}

// cliAnswer approves or denies a computer. Approving makes it a session of
// its own, which shows on the Members page and can be signed out.
func (a *API) cliAnswer(w http.ResponseWriter, r *http.Request) {
	code, answer := normalizeCode(r.PathValue("code")), r.PathValue("answer")
	if answer != "approve" && answer != "deny" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "approve or deny"})
		return
	}
	u := userFrom(r.Context())
	if u.ID == 0 {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "sign in to the dashboard to approve a computer"})
		return
	}
	a.logins.mu.Lock()
	defer a.logins.mu.Unlock()
	a.logins.sweep()
	lr := a.logins.m[code]
	if lr == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "That code isn't waiting any more. Run pier login again."})
		return
	}
	if lr.Status != "pending" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "This computer was already " + lr.Status + "."})
		return
	}
	if answer == "deny" {
		lr.Status = "denied"
		writeJSON(w, http.StatusOK, map[string]string{"status": "denied"})
		return
	}
	token, hash, err := auth.NewToken()
	if err != nil {
		a.fail(w, err)
		return
	}
	se := store.Session{
		TokenHash: hash, UserID: u.ID, Kind: store.SessionCLI, Name: "pier CLI on " + lr.Name,
		IP: lr.IP, ExpiresAt: time.Now().Add(auth.SessionFor),
	}
	if err := a.Store.CreateSession(r.Context(), se); err != nil {
		a.fail(w, err)
		return
	}
	lr.Status, lr.Token, lr.Email = "approved", token, u.Email
	a.Log.Info("computer approved", "name", lr.Name, "ip", lr.IP)
	writeJSON(w, http.StatusOK, map[string]string{"status": "approved"})
}
