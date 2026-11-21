package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"pier/internal/auth"
	"pier/internal/store"
	"pier/internal/version"
)

const (
	cookieName       = "pier_session"
	secureCookieName = "__Host-pier_session"
)

func sessionCookie(r *http.Request) (*http.Cookie, error) {
	if secure(r) {
		if c, err := r.Cookie(secureCookieName); err == nil {
			return c, nil
		}
	}
	return r.Cookie(cookieName)
}

// lookupSession finds the user behind a session token and records that the
// session was used. Sessions last 30 days from their last use.
func (a *API) lookupSession(ctx context.Context, token string, r *http.Request) (store.User, store.Session, bool) {
	if !strings.HasPrefix(token, "pier_") {
		return store.User{}, store.Session{}, false
	}
	se, err := a.Store.SessionByHash(ctx, auth.HashToken(token))
	if err != nil {
		return store.User{}, store.Session{}, false
	}
	u, err := a.Store.UserByID(ctx, se.UserID)
	if err != nil {
		return store.User{}, store.Session{}, false
	}
	// Writing on every request would be wasteful; once a minute is enough to
	// show "active now" and keep the session alive.
	if time.Since(se.LastSeenAt) > time.Minute {
		if err := a.Store.TouchSession(ctx, se.TokenHash, clientIP(r), time.Now().Add(auth.SessionFor)); err != nil {
			a.Log.Warn("touch session", "err", err)
		}
	}
	return u, se, true
}

func (a *API) setCookie(w http.ResponseWriter, r *http.Request, token string, maxAge int) {
	isSecure := secure(r)
	name := cookieName
	if isSecure {
		name = secureCookieName
	}
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: token, Path: "/", MaxAge: maxAge,
		HttpOnly: true, Secure: isSecure, SameSite: http.SameSiteLaxMode,
	})
	if maxAge < 0 && isSecure {
		http.SetCookie(w, &http.Cookie{
			Name: cookieName, Value: "", Path: "/", MaxAge: -1,
			HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
		})
	}
}

// refreshCookie keeps the browser's cookie in step with the session's 30 days.
func (a *API) refreshCookie(w http.ResponseWriter, r *http.Request, token string) {
	if r.Method == http.MethodGet && r.URL.Path == "/api/session" {
		a.setCookie(w, r, token, int(auth.SessionFor.Seconds()))
	}
}

type signInRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// signIn checks the owner's email and password and starts a browser session.
func (a *API) signIn(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Pier") == "" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "missing the X-Pier header"})
		return
	}
	var req signInRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "send an email and a password"})
		return
	}
	u, err := auth.SignIn(r.Context(), a.Store, req.Email, req.Password)
	var wrong auth.WrongError
	var locked auth.LockedError
	switch {
	case errors.As(err, &locked):
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error":        "Too many wrong passwords. Try again in a minute.",
			"locked_until": locked.Until,
		})
		return
	case errors.As(err, &wrong):
		body := map[string]any{"error": "That email and password don't match."}
		if wrong.Left > 0 {
			body["tries_left"] = wrong.Left
		}
		writeJSON(w, http.StatusUnauthorized, body)
		return
	case err != nil:
		a.fail(w, err)
		return
	}
	token, hash, err := auth.NewToken()
	if err != nil {
		a.fail(w, err)
		return
	}
	se := store.Session{
		TokenHash: hash, UserID: u.ID, Kind: store.SessionBrowser,
		Name: browserName(r.UserAgent()), IP: clientIP(r), ExpiresAt: time.Now().Add(auth.SessionFor),
	}
	if err := a.Store.CreateSession(r.Context(), se); err != nil {
		a.fail(w, err)
		return
	}
	a.Log.Info("signed in", "email", u.Email, "ip", se.IP)
	a.setCookie(w, r, token, int(auth.SessionFor.Seconds()))
	a.session(w, r.WithContext(context.WithValue(r.Context(), userKey, u)))
}

func (a *API) signOut(w http.ResponseWriter, r *http.Request) {
	if se, ok := r.Context().Value(sessionKey).(store.Session); ok {
		if err := a.Store.DeleteSession(r.Context(), se.TokenHash); err != nil {
			a.fail(w, err)
			return
		}
	}
	a.setCookie(w, r, "", -1)
	w.WriteHeader(http.StatusNoContent)
}

// session tells the dashboard who's signed in and how the server is set up.
func (a *API) session(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	address, _ := a.Store.Setting(ctx, store.SettingAddress)
	if address == "" {
		address = a.Deployer.PierHost
	}
	base, _ := a.Store.Setting(ctx, store.SettingBaseDomain)
	u := userFrom(ctx)
	var user any
	if u.ID != 0 {
		user = map[string]any{"name": u.Name, "email": u.Email}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user": user,
		"server": map[string]any{
			"address":           address,
			"base_domain":       base,
			"domain":            a.Deployer.Domain,
			"production_branch": a.Deployer.ProductionBranch,
			"version":           version.String(),
		},
	})
}

// versionInfo tells a signed-in client, such as pier version, which release
// of pierd it's talking to.
func (a *API) versionInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": version.String()})
}

// browserName turns a User-Agent into "Chrome on macOS", for the list of
// places you're signed in.
func browserName(ua string) string {
	browser := "A browser"
	for _, b := range []struct{ token, name string }{
		{"Edg/", "Edge"}, {"OPR/", "Opera"}, {"Firefox/", "Firefox"}, {"Chrome/", "Chrome"}, {"Safari/", "Safari"},
	} {
		if strings.Contains(ua, b.token) {
			browser = b.name
			break
		}
	}
	system := ""
	switch {
	case strings.Contains(ua, "iPhone"):
		system = "iPhone"
	case strings.Contains(ua, "iPad"):
		system = "iPad"
	case strings.Contains(ua, "Android"):
		system = "Android"
	case strings.Contains(ua, "Mac OS X"):
		system = "macOS"
	case strings.Contains(ua, "Windows"):
		system = "Windows"
	case strings.Contains(ua, "Linux"):
		system = "Linux"
	}
	if system == "" {
		return browser
	}
	return browser + " on " + system
}
