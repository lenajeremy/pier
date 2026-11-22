package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"pier/internal/auth"
	"pier/internal/store"
)

// A server has one owner, and the people the owner invites, each a member or
// a viewer. Roles hold across the whole server; access per project comes
// later.

type memberRow struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	Email        string    `json:"email"`
	Role         string    `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
	LastActiveAt time.Time `json:"last_active_at"`
	You          bool      `json:"you"`
}

// members lists everyone who can sign in to this server.
func (a *API) members(w http.ResponseWriter, r *http.Request) {
	users, err := a.Store.Users(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	me := userFrom(r.Context())
	out := []memberRow{}
	for _, u := range users {
		out = append(out, memberRow{
			ID: u.ID, Name: u.Name, Email: u.Email, Role: u.Role,
			CreatedAt: u.CreatedAt, LastActiveAt: u.LastActiveAt, You: u.ID == me.ID,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": out})
}

func memberID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "There's nobody with that ID on this server."})
		return 0, false
	}
	return id, true
}

// roleWord is a role as a sentence says it: "a member".
func roleWord(role string) string {
	if role == store.RoleViewer {
		return "a viewer"
	}
	return "a member"
}

// setRole makes someone a member or a viewer. It applies to their next
// request, in every browser and on every computer they use.
func (a *API) setRole(w http.ResponseWriter, r *http.Request) {
	id, ok := memberID(w, r)
	if !ok {
		return
	}
	var req struct {
		Role string `json:"role"`
	}
	json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req)
	if req.Role != store.RoleMember && req.Role != store.RoleViewer {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Choose member or viewer. A server has one owner.", "field": "role"})
		return
	}
	u, err := a.Store.SetRole(r.Context(), id, req.Role)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "There's nobody with that ID on this server."})
		return
	case errors.Is(err, store.ErrOwner):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "The owner stays the owner."})
		return
	case err != nil:
		a.fail(w, err)
		return
	}
	a.event(r.Context(), "", "Changed a role", u.DisplayName()+" is now "+roleWord(u.Role), "")
	a.Log.Info("role changed", "user", u.Email, "role", u.Role)
	writeJSON(w, http.StatusOK, map[string]any{"member": memberRow{
		ID: u.ID, Name: u.Name, Email: u.Email, Role: u.Role, CreatedAt: u.CreatedAt, LastActiveAt: u.LastActiveAt,
	}})
}

// removeMember deletes someone's account. Every browser they're signed in
// on and every computer they approved is signed out with it.
func (a *API) removeMember(w http.ResponseWriter, r *http.Request) {
	id, ok := memberID(w, r)
	if !ok {
		return
	}
	u, err := a.Store.RemoveUser(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "There's nobody with that ID on this server."})
		return
	case errors.Is(err, store.ErrOwner):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "The owner can't be removed."})
		return
	case err != nil:
		a.fail(w, err)
		return
	}
	a.event(r.Context(), "", "Removed someone", u.DisplayName()+", "+u.Email, "")
	a.Log.Info("member removed", "user", u.Email)
	w.WriteHeader(http.StatusNoContent)
}

// invites lists the invite links that haven't been used yet.
func (a *API) invites(w http.ResponseWriter, r *http.Request) {
	list, err := a.Store.Invites(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"invites": list})
}

// Invite tokens start with this, so one pasted somewhere it shouldn't be is
// easy to recognise, and can't be mistaken for a session's.
const invitePrefix = "pinv_"

// createInvite makes an invite link for one person to join with a role. The
// link is in the answer only: just its hash is kept.
func (a *API) createInvite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Role string `json:"role"`
	}
	json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req)
	if req.Role != store.RoleMember && req.Role != store.RoleViewer {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Invite them as a member or a viewer.", "field": "role"})
		return
	}
	token, _, err := auth.NewToken()
	if err != nil {
		a.fail(w, err)
		return
	}
	token = invitePrefix + strings.TrimPrefix(token, "pier_")
	// The server's own token has no account; its invites are the owner's.
	by := userFrom(r.Context()).ID
	if by == 0 {
		o, err := a.Store.Owner(r.Context())
		if err != nil {
			a.fail(w, err)
			return
		}
		by = o.ID
	}
	in, err := a.Store.CreateInvite(r.Context(), auth.HashToken(token), req.Role, by)
	if err != nil {
		a.fail(w, err)
		return
	}
	link := a.dashboardOrigin(r) + "/invite#" + token
	// Pier doesn't send email. When it does, this is where the link goes out,
	// to an address the owner gives with the invite; until then, the owner
	// copies the link and sends it themselves.
	a.event(r.Context(), "", "Invited someone", "As "+roleWord(in.Role), "")
	writeJSON(w, http.StatusCreated, map[string]any{"invite": in, "link": link})
}

// dashboardOrigin is where the owner is using the dashboard, so an invite
// link opens the same dashboard they made it in.
func (a *API) dashboardOrigin(r *http.Request) string {
	if o, err := url.Parse(r.Header.Get("Origin")); err == nil && (o.Scheme == "https" || o.Scheme == "http") && o.Host != "" && o.Path == "" {
		return o.Scheme + "://" + o.Host
	}
	scheme := "http"
	if secure(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// revokeInvite stops an invite link from working.
func (a *API) revokeInvite(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err == nil {
		err = a.Store.RevokeInvite(r.Context(), id)
	}
	if errors.Is(err, store.ErrNotFound) || id <= 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "That invite has already been used or revoked."})
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	a.event(r.Context(), "", "Revoked an invite", "", "")
	w.WriteHeader(http.StatusNoContent)
}

// goneInvite is what an invite that can't be used any more answers.
func goneInvite(w http.ResponseWriter) {
	writeJSON(w, http.StatusGone, map[string]string{"error": "This invite has been used, revoked or has run out. Ask the owner for a new one."})
}

// readInvite reads the token an invite page sends, and finds its invite.
// Like signing in, it needs X-Pier, so another site can't use it.
func (a *API) readInvite(w http.ResponseWriter, r *http.Request, into any) (store.Invite, string, bool) {
	if r.Header.Get("X-Pier") == "" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "missing the X-Pier header"})
		return store.Invite{}, "", false
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 8192))
	var t struct {
		Token string `json:"token"`
	}
	if err != nil || json.Unmarshal(raw, &t) != nil || (into != nil && json.Unmarshal(raw, into) != nil) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Open the whole invite link."})
		return store.Invite{}, "", false
	}
	if !strings.HasPrefix(t.Token, invitePrefix) {
		goneInvite(w)
		return store.Invite{}, "", false
	}
	hash := auth.HashToken(t.Token)
	in, err := a.Store.InviteByHash(r.Context(), hash)
	if errors.Is(err, store.ErrNotFound) {
		goneInvite(w)
		return in, "", false
	}
	if err != nil {
		a.fail(w, err)
		return in, "", false
	}
	return in, hash, true
}

// lookupInvite is what the invite page shows before anyone types: who
// invited them, as what, to which server, and until when.
func (a *API) lookupInvite(w http.ResponseWriter, r *http.Request) {
	in, _, ok := a.readInvite(w, r, nil)
	if !ok {
		return
	}
	address, _ := a.Store.Setting(r.Context(), store.SettingAddress)
	if address == "" {
		address = a.Deployer.PierHost
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"role": in.Role, "invited_by": in.CreatedBy, "expires_at": in.ExpiresAt, "server": address,
	})
}

// acceptInvite makes the invited person's account with the invite's role,
// uses the invite up, and signs them in.
func (a *API) acceptInvite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	_, hash, ok := a.readInvite(w, r, &req)
	if !ok {
		return
	}
	name, email := strings.TrimSpace(req.Name), strings.TrimSpace(req.Email)
	switch {
	case name == "":
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Enter your name, so people know who did what.", "field": "name"})
		return
	case utf8.RuneCountInString(name) > 80:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Use 80 characters or fewer.", "field": "name"})
		return
	case !auth.ValidEmail(email):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "That doesn't look like an email address.", "field": "email"})
		return
	}
	if err := auth.CheckPasswordRules(req.Password); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Use " + strings.TrimPrefix(err.Error(), "use ") + ".", "field": "password"})
		return
	}
	pw, err := auth.HashPassword(req.Password)
	if err != nil {
		a.fail(w, err)
		return
	}
	u, err := a.Store.AcceptInvite(r.Context(), hash, email, name, pw)
	switch {
	case errors.Is(err, store.ErrNotFound):
		goneInvite(w)
		return
	case errors.Is(err, store.ErrExists):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "There's already an account for that email. Sign in with it instead.", "field": "email"})
		return
	case err != nil:
		a.fail(w, err)
		return
	}
	ctx := context.WithValue(r.Context(), userKey, u)
	a.event(ctx, "", "Joined", "As "+roleWord(u.Role), "")
	a.Log.Info("joined", "email", u.Email, "role", u.Role)
	if !a.startSession(w, r.WithContext(ctx), u) {
		return
	}
	a.session(w, r.WithContext(ctx))
}
