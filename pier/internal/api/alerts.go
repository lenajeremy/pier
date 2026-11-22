package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"pier/internal/alerts"
	"pier/internal/dnscheck"
	"pier/internal/store"
)

// alertSettings is the Alerts page: each channel that's set up, without its
// secrets, who alert emails go to, and which events send one.
func (a *API) alertSettings(w http.ResponseWriter, r *http.Request) {
	if !a.alertsReady(w) {
		return
	}
	ctx := r.Context()
	chans, err := a.Alerts.Channels(ctx)
	if err != nil {
		a.fail(w, err)
		return
	}
	people, err := a.Store.AlertRecipients(ctx)
	if err != nil {
		a.fail(w, err)
		return
	}
	previews, err := a.Store.Setting(ctx, store.SettingPreviewAlerts)
	if err != nil {
		a.fail(w, err)
		return
	}
	limit, window := a.Alerts.Limits()
	writeJSON(w, http.StatusOK, map[string]any{
		"channels":   chans,
		"recipients": people,
		"previews":   previews != "off",
		"limit":      map[string]int{"alerts": limit, "minutes": int(window / time.Minute)},
	})
}

func (a *API) alertsReady(w http.ResponseWriter) bool {
	if a.Alerts == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Alerts aren't available on this server."})
		return false
	}
	return true
}

func alertKind(w http.ResponseWriter, r *http.Request) (string, bool) {
	kind := r.PathValue("kind")
	if !slices.Contains(alerts.Kinds, kind) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "There's no alert channel called " + kind + "."})
		return "", false
	}
	return kind, true
}

var channelName = map[string]string{alerts.Email: "email", alerts.Chat: "Slack or Discord", alerts.Webhook: "a webhook"}

// setAlertEvents turns alerts for failed previews on or off.
func (a *API) setAlertEvents(w http.ResponseWriter, r *http.Request) {
	if !a.alertsReady(w) {
		return
	}
	var req struct {
		Previews *bool `json:"previews"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil || req.Previews == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Say whether failed previews send an alert."})
		return
	}
	value := "on"
	if !*req.Previews {
		value = "off"
	}
	if err := a.Store.SetSetting(r.Context(), store.SettingPreviewAlerts, value); err != nil {
		a.fail(w, err)
		return
	}
	a.event(r.Context(), "", "Turned alerts for failed previews "+value, "", "")
	a.alertSettings(w, r)
}

type channelRequest struct {
	// Email
	Provider string `json:"provider"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"` // empty keeps the one that's saved
	APIKey   string `json:"api_key"`  // empty keeps the one that's saved
	From     string `json:"from"`
	// Slack, Discord and webhooks
	URL string `json:"url"` // empty keeps the one that's saved
}

func fieldError(w http.ResponseWriter, field, msg string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg, "field": field})
}

// setAlertChannel sets up or changes one channel. A secret left empty keeps
// the one that's saved, so a change never has to retype it. The first time
// a webhook is saved, its signing secret is made and shown, once.
func (a *API) setAlertChannel(w http.ResponseWriter, r *http.Request) {
	if !a.alertsReady(w) {
		return
	}
	kind, ok := alertKind(w, r)
	if !ok {
		return
	}
	var req channelRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 16384)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Send the channel's settings as JSON."})
		return
	}
	ctx := r.Context()
	kept, err := a.Alerts.Secrets(ctx, kind)
	if err != nil {
		a.fail(w, err)
		return
	}
	secrets := map[string]string{}
	var settings any
	shown := ""
	switch kind {
	case alerts.Email:
		s := alerts.EmailSettings{Provider: req.Provider, Host: strings.ToLower(strings.TrimSpace(req.Host)), Port: req.Port, User: strings.TrimSpace(req.User)}
		from, err := alerts.EmailAddress(req.From)
		if err != nil {
			fieldError(w, "from", err.Error())
			return
		}
		s.From = from
		switch s.Provider {
		case alerts.SMTP:
			if s.Host == "" || (s.Host != "localhost" && net.ParseIP(s.Host) == nil && !dnscheck.ValidDomain(s.Host)) {
				fieldError(w, "host", "Enter the mail server's name, like smtp.example.com.")
				return
			}
			if s.Port == 0 {
				s.Port = 587
			}
			if s.Port < 1 || s.Port > 65535 {
				fieldError(w, "port", "Use a port from 1 to 65535. Most servers use 587, or 465 for TLS from the start.")
				return
			}
			if s.User != "" {
				secrets["password"] = req.Password
				if req.Password == "" {
					secrets["password"] = kept["password"]
				}
				if secrets["password"] == "" {
					fieldError(w, "password", "Enter the password for "+s.User+".")
					return
				}
			}
			s.Host = strings.TrimSuffix(s.Host, ".")
		case alerts.Resend:
			s.Host, s.Port, s.User = "", 0, ""
			secrets["api_key"] = strings.TrimSpace(req.APIKey)
			if secrets["api_key"] == "" {
				secrets["api_key"] = kept["api_key"]
			}
			if secrets["api_key"] == "" {
				fieldError(w, "api_key", "Paste a Resend API key. It starts with re_.")
				return
			}
		default:
			fieldError(w, "provider", "Choose SMTP or Resend.")
			return
		}
		settings = s
	case alerts.Chat:
		url := strings.TrimSpace(req.URL)
		if url == "" {
			url = kept["url"]
		}
		service, err := alerts.ChatService(url)
		if err != nil {
			fieldError(w, "url", err.Error())
			return
		}
		secrets["url"] = url
		settings = alerts.ChatSettings{Service: service}
	case alerts.Webhook:
		url := strings.TrimSpace(req.URL)
		if url == "" {
			url = kept["url"]
		}
		if err := alerts.WebhookURL(url); err != nil {
			fieldError(w, "url", err.Error())
			return
		}
		secrets["url"] = url
		secrets["signing"] = kept["signing"]
		if secrets["signing"] == "" {
			secrets["signing"] = alerts.NewSigningSecret()
			shown = secrets["signing"]
		}
		settings = struct{}{}
	}
	if err := a.Alerts.Save(ctx, kind, settings, secrets); err != nil {
		a.fail(w, err)
		return
	}
	a.event(ctx, "", "Set up alerts to "+channelName[kind], "", "")
	a.channelAnswer(w, r, kind, shown)
}

// channelAnswer is a channel as the dashboard sees it, and a webhook's new
// signing secret the one time it's shown.
func (a *API) channelAnswer(w http.ResponseWriter, r *http.Request, kind, signing string) {
	chans, err := a.Alerts.Channels(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	out := map[string]any{"channel": chans[kind]}
	if signing != "" {
		out["signing_secret"] = signing
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) deleteAlertChannel(w http.ResponseWriter, r *http.Request) {
	if !a.alertsReady(w) {
		return
	}
	kind, ok := alertKind(w, r)
	if !ok {
		return
	}
	had, err := a.Store.DeleteAlertChannel(r.Context(), kind)
	if err != nil {
		a.fail(w, err)
		return
	}
	if had {
		a.event(r.Context(), "", "Stopped alerts to "+channelName[kind], "", "")
	}
	w.WriteHeader(http.StatusNoContent)
}

// testAlertChannel sends a test alert at once and answers with exactly what
// went wrong, if anything did.
func (a *API) testAlertChannel(w http.ResponseWriter, r *http.Request) {
	if !a.alertsReady(w) {
		return
	}
	kind, ok := alertKind(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	err := a.Alerts.Test(ctx, kind)
	if errors.Is(err, alerts.ErrNotSetUp) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Set this channel up before testing it."})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"sent": true})
}

// newWebhookSecret replaces a webhook's signing secret, and shows the new
// one once. Receivers need it before the next alert.
func (a *API) newWebhookSecret(w http.ResponseWriter, r *http.Request) {
	if !a.alertsReady(w) {
		return
	}
	ctx := r.Context()
	kept, err := a.Alerts.Secrets(ctx, alerts.Webhook)
	if err != nil {
		a.fail(w, err)
		return
	}
	if kept["url"] == "" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Set the webhook up first."})
		return
	}
	kept["signing"] = alerts.NewSigningSecret()
	if err := a.Alerts.Save(ctx, alerts.Webhook, struct{}{}, kept); err != nil {
		a.fail(w, err)
		return
	}
	a.event(ctx, "", "Replaced the webhook's signing secret", "", "")
	a.channelAnswer(w, r, alerts.Webhook, kept["signing"])
}

// accountAlerts is whether the signed-in person gets alert emails, and
// changes it with PUT.
func (a *API) accountAlerts(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.ID == 0 {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Sign in with your account to choose your alert emails."})
		return
	}
	// An alert quotes a build's or a job's output as it was, values and
	// all, so a viewer has no switch to turn on.
	if u.Role == store.RoleViewer && r.Method == http.MethodPut {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Viewers don't get alert emails, because an alert can quote a variable's value. Ask the owner to make you a member.", "role": u.Role, "need": store.RoleMember})
		return
	}
	if r.Method == http.MethodPut {
		var req struct {
			Email *bool `json:"email"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil || req.Email == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Say whether you want alert emails."})
			return
		}
		if err := a.Store.SetAlertEmails(r.Context(), u.ID, *req.Email); err != nil {
			a.fail(w, err)
			return
		}
	}
	on, err := a.Store.AlertEmails(r.Context(), u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"email": on})
}
