package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"pier/internal/deploy"
	"pier/internal/store"
)

var varName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,255}$`)

// reservedVar names are set by Pier for every build, or would break it.
var reservedVar = map[string]bool{"PATH": true, "HOME": true, "CI": true, "PWD": true}

// MaxValue is the longest a variable's value may be.
const MaxValue = 64 << 10

type varEntry struct {
	Name      string    `json:"name"`
	Scopes    []string  `json:"scopes"`
	UpdatedAt time.Time `json:"updated_at"`
}

// variables lists a project's variables without their values: one entry
// per name, or two when production and previews hold different values. It
// also says which ones changed since production last built.
func (a *API) variables(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	ps, ok := a.findProject(w, r, name)
	if !ok {
		return
	}
	all, err := a.Store.Variables(r.Context(), name)
	if err != nil {
		a.fail(w, err)
		return
	}
	entries := []varEntry{}
	byName := map[string][]store.Variable{}
	var order []string
	for _, v := range all {
		if byName[v.Name] == nil {
			order = append(order, v.Name)
		}
		byName[v.Name] = append(byName[v.Name], v)
	}
	for _, n := range order {
		vs := byName[n]
		sort.Slice(vs, func(i, j int) bool { return vs[i].Scope == store.TargetProduction && vs[j].Scope != store.TargetProduction })
		if len(vs) == 2 && a.sameValue(name, vs[0], vs[1]) {
			e := varEntry{Name: n, Scopes: []string{store.TargetProduction, store.TargetPreview}, UpdatedAt: vs[0].UpdatedAt}
			if vs[1].UpdatedAt.After(e.UpdatedAt) {
				e.UpdatedAt = vs[1].UpdatedAt
			}
			entries = append(entries, e)
			continue
		}
		for _, v := range vs {
			entries = append(entries, varEntry{Name: n, Scopes: []string{v.Scope}, UpdatedAt: v.UpdatedAt})
		}
	}

	// What production's build doesn't have yet.
	stale := map[string]any{"production": false, "names": []string{}}
	if ps.production != nil && ps.production.Deployment != nil {
		built := ps.production.Deployment.CreatedAt
		changed, _, err := a.Store.VariablesChanged(r.Context(), name)
		if err == nil && changed.After(built) {
			var names []string
			for _, v := range all {
				if v.Scope == store.TargetProduction && v.UpdatedAt.After(built) {
					names = append(names, v.Name)
				}
			}
			stale = map[string]any{"production": true, "names": nonNil(names), "deployment": ps.production.Deployment.ID}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"variables": entries, "stale": stale})
}

func (a *API) sameValue(project string, x, y store.Variable) bool {
	vx, err1 := a.Deployer.Secrets.Open(x.Value, deploy.SealedFor(project, x.Scope, x.Name))
	vy, err2 := a.Deployer.Secrets.Open(y.Value, deploy.SealedFor(project, y.Scope, y.Name))
	return err1 == nil && err2 == nil && vx == vy
}

type varRequest struct {
	Name   string   `json:"name"`
	Value  string   `json:"value"`
	Scopes []string `json:"scopes"`
}

func readVar(w http.ResponseWriter, r *http.Request, needValue bool) (varRequest, bool) {
	var req varRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, MaxValue+4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "send the variable as JSON"})
		return req, false
	}
	req.Name = strings.TrimSpace(req.Name)
	switch {
	case !varName.MatchString(req.Name):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Use letters, numbers and underscores, starting with a letter, like API_URL.", "field": "name"})
		return req, false
	case reservedVar[req.Name] || strings.HasPrefix(req.Name, "npm_config_") || strings.HasPrefix(req.Name, "COREPACK_"):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": req.Name + " is set by Pier for every build. Choose another name.", "field": "name"})
		return req, false
	case needValue && len(req.Value) > MaxValue:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Values can be up to 64 KB.", "field": "value"})
		return req, false
	}
	if len(req.Scopes) == 0 {
		req.Scopes = []string{store.TargetProduction, store.TargetPreview}
	}
	for _, s := range req.Scopes {
		if s != store.TargetProduction && s != store.TargetPreview {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Scopes are production and preview.", "field": "scopes"})
			return req, false
		}
	}
	return req, true
}

// setVariable adds or changes a variable in the scopes given.
func (a *API) setVariable(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	if _, ok := a.findProject(w, r, name); !ok {
		return
	}
	req, ok := readVar(w, r, true)
	if !ok {
		return
	}
	for _, scope := range req.Scopes {
		sealed, err := a.Deployer.Secrets.Seal(req.Value, deploy.SealedFor(name, scope, req.Name))
		if err != nil {
			a.fail(w, err)
			return
		}
		if err := a.Store.SetVariable(r.Context(), store.Variable{Project: name, Scope: scope, Name: req.Name, Value: sealed}); err != nil {
			a.fail(w, err)
			return
		}
	}
	a.Log.Info("variable set", "project", name, "name", req.Name, "scopes", strings.Join(req.Scopes, ","))
	a.variables(w, r)
}

// deleteVariable removes a variable from the scopes given.
func (a *API) deleteVariable(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	if _, ok := a.findProject(w, r, name); !ok {
		return
	}
	req, ok := readVar(w, r, false)
	if !ok {
		return
	}
	for _, scope := range req.Scopes {
		if _, err := a.Store.DeleteVariable(r.Context(), name, scope, req.Name); err != nil {
			a.fail(w, err)
			return
		}
	}
	a.Log.Info("variable deleted", "project", name, "name", req.Name)
	a.variables(w, r)
}

// revealVariable returns one value, when the owner asks to see it.
func (a *API) revealVariable(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	if _, ok := a.findProject(w, r, name); !ok {
		return
	}
	req, ok := readVar(w, r, false)
	if !ok || len(req.Scopes) == 0 {
		return
	}
	all, err := a.Store.Variables(r.Context(), name)
	if err != nil {
		a.fail(w, err)
		return
	}
	i := slices.IndexFunc(all, func(v store.Variable) bool { return v.Name == req.Name && v.Scope == req.Scopes[0] })
	if i < 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "There's no " + req.Name + " in " + req.Scopes[0] + "."})
		return
	}
	value, err := a.Deployer.Secrets.Open(all[i].Value, deploy.SealedFor(name, req.Scopes[0], req.Name))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"value": value})
}

// redeploy rebuilds a deployment's source with today's settings and
// variables: production's, when no deployment is named. It answers as soon
// as the new deployment exists; its page follows the build.
func (a *API) redeploy(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	ps, ok := a.findProject(w, r, name)
	if !ok {
		return
	}
	id := readID(r)
	target := ""
	if id == "" {
		if ps.production == nil || ps.production.Deployment == nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "Production isn't serving anything yet, so there's nothing to redeploy."})
			return
		}
		id, target = ps.production.Deployment.ID, store.TargetProduction
	}
	started := make(chan string, 1)
	failed := make(chan error, 1)
	go func() {
		err := a.Deployer.Redeploy(context.WithoutCancel(r.Context()), name, id, target, func(newID string) { started <- newID }, io.Discard)
		if err != nil {
			a.Log.Info("redeploy failed", "project", name, "from", id, "err", err)
			failed <- err
		}
	}()
	select {
	case newID := <-started:
		writeJSON(w, http.StatusAccepted, map[string]string{"id": newID, "from": id})
	case err := <-failed:
		if errors.Is(err, deploy.ErrNoSource) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": id + "'s source isn't kept any more, so it can't be rebuilt. Deploy again from your computer."})
			return
		}
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Couldn't start the build: " + err.Error()})
	case <-time.After(30 * time.Second):
		writeJSON(w, http.StatusAccepted, map[string]string{"from": id, "note": "the build is waiting for another one on this project to finish"})
	}
}
