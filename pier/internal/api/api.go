// Package api is pierd's HTTP API. The public side is what the CLI and the
// dashboard use; the internal side listens only on a local socket, for the
// git hook.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"pier/internal/alerts"
	"pier/internal/board"
	"pier/internal/database"
	"pier/internal/deploy"
	"pier/internal/hook"
	"pier/internal/jobs"
	"pier/internal/release"
	"pier/internal/requests"
	"pier/internal/store"
)

type API struct {
	StartedAt      time.Time
	healthMu       sync.Mutex
	healthAt       time.Time
	healthSnapshot map[string]any
	cacheSize      cacheMeter
	// folders keeps folder listings, by repository and commit.
	folders        folderCache
	SiteListener   string // local site listener for TLS handshakes with the requested SNI
	githubSetup    sync.Mutex
	githubEndpoint string // GitHub API base for integration tests
	logins         logins // pier login requests waiting for approval
	Store          *store.Store
	Deployer       *deploy.Deployer
	Token          string // the server's own token, from pierd token; still accepted for the CLI
	Log            *slog.Logger
	Web            http.Handler  // the dashboard's files; nil serves the API only
	Requests       *requests.Log // every request the sites answered; nil keeps none
	Alerts         *alerts.Notifier
	Jobs           *jobs.Scheduler
	Databases      *database.Manager // each project's Postgres; nil when this server has none
	Releases       *release.Checker // which release is the newest; nil never says
	Data           string           // pierd's data folder, where an update is asked for
	UpgradeUnit    string           // the update helper's unit file, if it's installed
}

// BoardRow is a board row with the address's full URL.
type BoardRow struct {
	board.Row
	URL string `json:"url"`
}

// Public serves the dashboard and the API, every route behind its guard.
func (a *API) Public() http.Handler {
	return securityHeaders(a.serve(a.routes()))
}

// Internal serves the git hook, on pierd's local socket only.
func (a *API) Internal() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /internal/push", a.push)
	return mux
}

type ctxKey int

const (
	userKey ctxKey = iota
	sessionKey
)

// userFrom returns the signed-in user. For the server's own token it has no
// ID and the owner's role: only someone on the server can read that token.
func userFrom(ctx context.Context) store.User {
	u, _ := ctx.Value(userKey).(store.User)
	return u
}

// auth lets a request through with a session cookie (the dashboard), a
// session token (the CLI), or the server's own token. Cookie requests that
// change something must carry X-Pier, which another site's page can't send
// without permission, so a signed-in browser can't be tricked into acting.
// The user, and so their role, is read from the database on every request.
func (a *API) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
			if a.Token != "" && subtle.ConstantTimeCompare([]byte(bearer), []byte(a.Token)) == 1 {
				next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, userKey, store.User{Role: store.RoleOwner})))
				return
			}
			if u, se, ok := a.lookupSession(ctx, bearer, r); ok {
				ctx = context.WithValue(context.WithValue(ctx, userKey, u), sessionKey, se)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "that token isn't valid any more; run pier login"})
			return
		}
		c, err := sessionCookie(r)
		if err == nil {
			if u, se, ok := a.lookupSession(ctx, c.Value, r); ok {
				if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-Pier") == "" {
					writeJSON(w, http.StatusForbidden, map[string]string{"error": "missing the X-Pier header"})
					return
				}
				a.refreshCookie(w, r, c.Value)
				ctx = context.WithValue(context.WithValue(ctx, userKey, u), sessionKey, se)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "sign in first"})
	})
}

func (a *API) board(w http.ResponseWriter, r *http.Request) {
	aliases, err := a.Store.Aliases(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	latest, err := a.Store.LatestPerBranch(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	rows := []BoardRow{}
	for _, row := range board.Rows(aliases, latest, a.Deployer.Domain, a.Deployer.ProductionBranch) {
		rows = append(rows, BoardRow{Row: row, URL: a.Deployer.URL(row.Address)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"domain": a.Deployer.Domain, "rows": rows})
}

// push deploys one pushed branch and streams the narration back to the hook.
// The build carries on if the pusher disconnects.
func (a *API) push(w http.ResponseWriter, r *http.Request) {
	var p hook.Push
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&p); err != nil || !validProject(p.Project) || p.Branch == "" || !validCommit(p.Commit) {
		http.Error(w, "pier    bad push request\n", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Trailer", "Pier-Result")
	w.WriteHeader(http.StatusOK)
	out := &flusher{w: w}
	err := a.Deployer.Deploy(context.WithoutCancel(r.Context()), p.Project, p.Branch, p.Commit, out)
	if err != nil {
		a.Log.Info("deploy failed", "project", p.Project, "branch", p.Branch, "err", err)
		w.Header().Set("Pier-Result", "failed")
		return
	}
	w.Header().Set("Pier-Result", "ok")
}

// flusher sends every line to the pusher as soon as it's written. Write
// errors (the pusher went away) are swallowed so the build can finish.
type flusher struct {
	w    http.ResponseWriter
	gone bool
}

func (f *flusher) Write(p []byte) (int, error) {
	if !f.gone {
		if _, err := f.w.Write(p); err != nil {
			f.gone = true
		} else if err := http.NewResponseController(f.w).Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
			f.gone = true
		}
	}
	return len(p), nil
}

func (a *API) fail(w http.ResponseWriter, err error) {
	a.Log.Error("api", "err", err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "something went wrong on the server; check pierd's log"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func validCommit(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		if secure(r) {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP is the address a request came from. Behind the router (a proxy on
// this machine) it's the first address in X-Forwarded-For.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			first, _, _ := strings.Cut(fwd, ",")
			return strings.TrimSpace(first)
		}
	}
	return host
}

// secure says whether the request reached Pier over HTTPS, directly or
// through the router on this machine.
func secure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return r.Header.Get("X-Forwarded-Proto") == "https"
	}
	return false
}
