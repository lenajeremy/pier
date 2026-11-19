package api

import (
	"net/http"
	"time"

	"pier/internal/store"
)

// A session's ID on the page is the start of its token's hash: enough to
// tell sessions apart, useless for signing in.
const sessionIDLen = 16

// sessions lists where the owner is signed in: browsers and computers.
func (a *API) sessions(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	current, _ := r.Context().Value(sessionKey).(store.Session)
	list, err := a.Store.Sessions(r.Context(), u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	type row struct {
		ID         string    `json:"id"`
		Kind       string    `json:"kind"`
		Name       string    `json:"name"`
		IP         string    `json:"ip"`
		CreatedAt  time.Time `json:"created_at"`
		LastSeenAt time.Time `json:"last_seen_at"`
		Current    bool      `json:"current"`
	}
	out := []row{}
	for _, se := range list {
		out = append(out, row{
			ID: se.TokenHash[:sessionIDLen], Kind: se.Kind, Name: se.Name, IP: se.IP,
			CreatedAt: se.CreatedAt, LastSeenAt: se.LastSeenAt, Current: se.TokenHash == current.TokenHash,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": map[string]any{"name": u.Name, "email": u.Email, "created_at": u.CreatedAt}, "sessions": out})
}

// signOutSession signs one browser or computer out.
func (a *API) signOutSession(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	id := r.PathValue("id")
	list, err := a.Store.Sessions(r.Context(), u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	for _, se := range list {
		if len(id) == sessionIDLen && se.TokenHash[:sessionIDLen] == id {
			if err := a.Store.DeleteSession(r.Context(), se.TokenHash); err != nil {
				a.fail(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "that session has already ended"})
}

// signOutOthers signs out everywhere except here.
func (a *API) signOutOthers(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	current, _ := r.Context().Value(sessionKey).(store.Session)
	n, err := a.Store.DeleteSessions(r.Context(), u.ID, current.TokenHash)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"signed_out": n})
}
