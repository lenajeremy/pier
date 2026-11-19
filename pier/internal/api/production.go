package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"pier/internal/deploy"
	"pier/internal/store"
)

// rollbackTarget is where Roll back goes: the newest production deployment
// that built, older than the one production serves now.
func (ps projectState) rollbackTarget() string {
	if ps.production == nil || ps.production.Deployment == nil {
		return ""
	}
	current := ps.production.Deployment
	for _, d := range ps.deployments {
		if d.ID != current.ID && d.Target == store.TargetProduction && d.Status == store.Ready && d.CreatedAt.Before(current.CreatedAt) {
			return d.ID
		}
	}
	return ""
}

func readID(r *http.Request) string {
	var body struct {
		ID string `json:"id"`
	}
	json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&body)
	return body.ID
}

// promote points production at any deployment that built, and lets
// production follow its builds again if it was rolled back.
func (a *API) promote(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	if _, ok := a.findProject(w, r, name); !ok {
		return
	}
	id := readID(r)
	if !a.moveProduction(w, r, name, id) {
		return
	}
	if err := a.Store.SetRolledBack(r.Context(), name, ""); err != nil {
		a.fail(w, err)
		return
	}
	a.Log.Info("promoted", "project", name, "deployment", id)
	a.project(w, r)
}

// rollback points production back at an older deployment: the one before
// it, or one named. Production builds then wait until the owner promotes
// one or undoes the rollback.
func (a *API) rollback(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	ps, ok := a.findProject(w, r, name)
	if !ok {
		return
	}
	if ps.production == nil || ps.production.Deployment == nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Production isn't serving anything yet, so there's nothing to roll back."})
		return
	}
	id := readID(r)
	if id == "" {
		id = ps.rollbackTarget()
	}
	if id == "" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "There's no earlier production deployment to roll back to."})
		return
	}
	current := ps.production.Deployment.ID
	if !a.moveProduction(w, r, name, id) {
		return
	}
	p, err := a.Store.Project(r.Context(), name)
	if err != nil {
		a.fail(w, err)
		return
	}
	if p.RolledBackFrom == "" { // a second rollback still undoes to where it all began
		if err := a.Store.SetRolledBack(r.Context(), name, current); err != nil {
			a.fail(w, err)
			return
		}
	}
	a.Log.Info("rolled back", "project", name, "from", current, "to", id)
	a.project(w, r)
}

// undoRollback puts production back where it was before the rollback.
func (a *API) undoRollback(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	if _, ok := a.findProject(w, r, name); !ok {
		return
	}
	p, err := a.Store.Project(r.Context(), name)
	if err != nil {
		a.fail(w, err)
		return
	}
	if p.RolledBackFrom == "" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Production isn't rolled back."})
		return
	}
	if !a.moveProduction(w, r, name, p.RolledBackFrom) {
		return
	}
	if err := a.Store.SetRolledBack(r.Context(), name, ""); err != nil {
		a.fail(w, err)
		return
	}
	a.Log.Info("rollback undone", "project", name, "deployment", p.RolledBackFrom)
	a.project(w, r)
}

func (a *API) moveProduction(w http.ResponseWriter, r *http.Request, name, id string) bool {
	err := a.Deployer.SetProduction(r.Context(), name, id)
	if errors.Is(err, deploy.ErrNotServable) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": name + " has no deployment " + id + " that built, so it can't serve production."})
		return false
	}
	if err != nil {
		a.fail(w, err)
		return false
	}
	return true
}
