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
	"net/http"
	"strings"

	"pier/internal/board"
	"pier/internal/deploy"
	"pier/internal/hook"
	"pier/internal/names"
	"pier/internal/store"
)

type API struct {
	Store    *store.Store
	Deployer *deploy.Deployer
	Token    string
	Log      *slog.Logger
}

// BoardRow is a board row with the address's full URL.
type BoardRow struct {
	board.Row
	URL string `json:"url"`
}

// Public serves the authenticated API.
func (a *API) Public() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok\n") })
	mux.Handle("GET /api/board", a.auth(http.HandlerFunc(a.board)))
	mux.Handle("GET /api/projects", a.auth(http.HandlerFunc(a.projects)))
	mux.Handle("GET /api/projects/{project}/deployments", a.auth(http.HandlerFunc(a.deployments)))
	return mux
}

// Internal serves the git hook, on pierd's local socket only.
func (a *API) Internal() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /internal/push", a.push)
	return mux
}

func (a *API) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(got), []byte(a.Token)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing or wrong token; run `pierd token` on the server and `pier login`"})
			return
		}
		next.ServeHTTP(w, r)
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

func (a *API) projects(w http.ResponseWriter, r *http.Request) {
	ps, err := a.Store.Projects(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	if ps == nil {
		ps = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": ps})
}

func (a *API) deployments(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	if !names.ValidProject(project) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "that isn't a project name"})
		return
	}
	ds, err := a.Store.Deployments(r.Context(), project)
	if err != nil {
		a.fail(w, err)
		return
	}
	if ds == nil {
		ds = []store.Deployment{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"project": project, "deployments": ds})
}

// push deploys one pushed branch and streams the narration back to the hook.
// The build carries on if the pusher disconnects.
func (a *API) push(w http.ResponseWriter, r *http.Request) {
	var p hook.Push
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&p); err != nil || !names.ValidProject(p.Project) || p.Branch == "" || p.Commit == "" {
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
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
