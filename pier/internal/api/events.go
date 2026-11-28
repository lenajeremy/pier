package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"pier/internal/requests"
	"pier/internal/router"
	"pier/internal/store"
)

// Events an app sends: a server posts to /api/events with the token its
// deploy was given; a browser posts to /_pier/event on the app's own
// address, which Caddy hands to pierd with the deployment it came from.

const eventsSecretKey = "events_secret"

func eventsSecret(ctx context.Context, st *store.Store) ([]byte, error) {
	v, err := st.Setting(ctx, eventsSecretKey)
	if err != nil {
		return nil, err
	}
	if v == "" {
		b := make([]byte, 32)
		rand.Read(b)
		v = hex.EncodeToString(b)
		if err := st.SetSetting(ctx, eventsSecretKey, v); err != nil {
			return nil, err
		}
		if again, err := st.Setting(ctx, eventsSecretKey); err == nil && again != "" {
			v = again
		}
	}
	return hex.DecodeString(v)
}

func eventsSig(secret []byte, project, env string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte("events:" + project + ":" + env))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:18])
}

// EventsToken is the token a deploy of project for env sends events with.
func EventsToken(ctx context.Context, st *store.Store, project, env string) (string, error) {
	secret, err := eventsSecret(ctx, st)
	if err != nil {
		return "", err
	}
	return "pier_ev_" + project + "." + env + "." + eventsSig(secret, project, env), nil
}

func (a *API) eventsTokenOwner(ctx context.Context, token string) (project, env string, ok bool) {
	rest, found := strings.CutPrefix(token, "pier_ev_")
	if !found {
		return "", "", false
	}
	parts := strings.Split(rest, ".")
	if len(parts) != 3 {
		return "", "", false
	}
	secret, err := eventsSecret(ctx, a.Store)
	if err != nil {
		return "", "", false
	}
	if !hmac.Equal([]byte(parts[2]), []byte(eventsSig(secret, parts[0], parts[1]))) {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// eventLimit keeps one project from flooding the event log: 6,000 events a
// minute each.
type eventLimit struct {
	mu     sync.Mutex
	minute int64
	used   map[string]int
}

func (l *eventLimit) allow(project string, n int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	m := time.Now().Unix() / 60
	if m != l.minute || l.used == nil {
		l.minute, l.used = m, map[string]int{}
	}
	if l.used[project]+n > 6000 {
		return false
	}
	l.used[project] += n
	return true
}

var limits eventLimit

// readEvents takes one event or a list of up to 50.
func readEvents(r io.Reader) ([]requests.Event, []requests.Sent, error) {
	body, err := io.ReadAll(io.LimitReader(r, 64<<10))
	if err != nil {
		return nil, nil, err
	}
	var many []requests.Sent
	if t := strings.TrimSpace(string(body)); strings.HasPrefix(t, "[") {
		if err := json.Unmarshal(body, &many); err != nil {
			return nil, nil, errors.New("send an event as JSON, or a list of them")
		}
	} else {
		var one requests.Sent
		if err := json.Unmarshal(body, &one); err != nil {
			return nil, nil, errors.New("send an event as JSON, or a list of them")
		}
		many = []requests.Sent{one}
	}
	if len(many) == 0 || len(many) > 50 {
		return nil, nil, errors.New("send 1 to 50 events at a time")
	}
	out := make([]requests.Event, 0, len(many))
	for _, s := range many {
		e, err := s.Check()
		if err != nil {
			return nil, nil, err
		}
		out = append(out, e)
	}
	return out, many, nil
}

// sendEvents is POST /api/events from an app's server.
func (a *API) sendEvents(w http.ResponseWriter, r *http.Request) {
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	project, env, ok := a.eventsTokenOwner(r.Context(), strings.TrimSpace(token))
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "send the PIER_EVENTS_TOKEN your deploy was given, as a Bearer token"})
		return
	}
	events, sent, err := readEvents(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if !limits.allow(project, len(events)) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "over 6,000 events a minute; try again in a minute"})
		return
	}
	if a.Requests != nil {
		for i, e := range events {
			e.Project, e.Env = project, env
			a.Requests.AddEvent(e, "", "", sent[i].User)
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]int{"accepted": len(events)})
}

// BrowserEvents answers /_pier/event on every app's address, through
// Caddy, on a listener only Caddy reaches.
func (a *API) BrowserEvents() http.Handler {
	var mu sync.Mutex
	known := map[string][2]string{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "send events with POST", http.StatusMethodNotAllowed)
			return
		}
		dep := r.Header.Get(router.EventHeader)
		mu.Lock()
		pt, ok := known[dep]
		mu.Unlock()
		if !ok {
			d, err := a.Store.Deployment(r.Context(), dep)
			if err != nil {
				http.Error(w, "no such deployment", http.StatusNotFound)
				return
			}
			pt = [2]string{d.Project, d.Target}
			mu.Lock()
			known[dep] = pt
			mu.Unlock()
		}
		events, _, err := readEvents(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if !limits.allow(pt[0], len(events)) {
			http.Error(w, "too many events", http.StatusTooManyRequests)
			return
		}
		ip, _, _ := net.SplitHostPort(r.RemoteAddr)
		if f := r.Header.Get("X-Forwarded-For"); f != "" {
			ip = strings.TrimSpace(strings.Split(f, ",")[0])
		}
		if a.Requests != nil {
			for _, e := range events {
				e.Project, e.Env = pt[0], pt[1]
				a.Requests.AddEvent(e, ip, r.UserAgent(), "")
			}
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func envOf(w http.ResponseWriter, r *http.Request) (string, bool) {
	env := r.URL.Query().Get("env")
	if env != "" && env != "production" && env != "preview" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "env is production or preview"})
		return "", false
	}
	return env, true
}

func eventSpan(r *http.Request) (int64, int64) {
	from, to := span(r)
	if oldest := time.Now().Add(-requests.KeepEvents).UnixMilli(); from < oldest {
		from = oldest
	}
	return from, to
}

// projectEvents lists the kinds of event a project's apps sent.
func (a *API) projectEvents(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	if _, ok := a.findProject(w, r, name); !ok {
		return
	}
	env, ok := envOf(w, r)
	if !ok {
		return
	}
	from, to := eventSpan(r)
	rows := []requests.EventRow{}
	if a.Requests != nil {
		var err error
		if rows, err = a.Requests.Events(r.Context(), name, env, from, to); err != nil {
			a.fail(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": rows, "from": from, "to": to})
}

// projectEvent is one kind of event over time, split by a property.
func (a *API) projectEvent(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	if _, ok := a.findProject(w, r, name); !ok {
		return
	}
	env, ok := envOf(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	from, to := eventSpan(r)
	if a.Requests == nil {
		writeJSON(w, http.StatusOK, requests.EventDetail{Name: q.Get("name")})
		return
	}
	d, err := a.Requests.Event(r.Context(), name, env, q.Get("name"), q.Get("by"), from, to)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func funnelKey(project string) string { return "events_funnel:" + project }

// projectFunnel counts the people who went through the project's funnel.
func (a *API) projectFunnel(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	if _, ok := a.findProject(w, r, name); !ok {
		return
	}
	env, ok := envOf(w, r)
	if !ok {
		return
	}
	from, to := eventSpan(r)
	steps := []string{}
	if v, _ := a.Store.Setting(r.Context(), funnelKey(name)); v != "" {
		json.Unmarshal([]byte(v), &steps)
	}
	counts := make([]int64, len(steps))
	names := []string{}
	if a.Requests != nil {
		var err error
		if len(steps) > 0 {
			if counts, err = a.Requests.Funnel(r.Context(), name, env, steps, from, to); err != nil {
				a.fail(w, err)
				return
			}
		}
		if names, err = a.Requests.EventNames(r.Context(), name, from, to); err != nil {
			a.fail(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"steps": steps, "counts": counts, "names": names})
}

// setFunnel chooses the funnel's steps: the visit step or event names, two
// to five of them.
func (a *API) setFunnel(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	if _, ok := a.findProject(w, r, name); !ok {
		return
	}
	var req struct {
		Steps []string `json:"steps"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req) != nil || len(req.Steps) > 5 || len(req.Steps) == 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "a funnel has 2 to 5 steps, or none"})
		return
	}
	for _, s := range req.Steps {
		if s == requests.VisitStep {
			continue
		}
		if _, err := (requests.Sent{Name: s}).Check(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "a step is an event's name"})
			return
		}
	}
	b, _ := json.Marshal(req.Steps)
	if err := a.Store.SetSetting(r.Context(), funnelKey(name), string(b)); err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"steps": req.Steps})
}
