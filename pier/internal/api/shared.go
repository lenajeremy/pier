package api

import (
	"errors"
	"net/http"
	"pier/internal/deploy"
	"pier/internal/store"
	"slices"
)

func (a *API) shared(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		rows, err := a.Store.SharedVariables(r.Context())
		if err != nil {
			a.fail(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"variables": rows})
		return
	}
	create, remove := r.Method == "POST", r.Method == "DELETE"
	v := store.SharedVariable{Name: r.PathValue("name")}
	if !remove {
		req, ok := readVar(w, r, true)
		if !ok {
			return
		}
		if !create && v.Name != req.Name {
			writeJSON(w, 400, map[string]string{"error": "The variable name must match."})
			return
		}
		v.Name = req.Name
		v.Production = slices.Contains(req.Scopes, "production")
		v.Preview = slices.Contains(req.Scopes, "preview")
		projects, err := a.Store.Projects(r.Context())
		if err != nil {
			a.fail(w, err)
			return
		}
		for _, p := range req.Projects {
			if !slices.Contains(projects, p) {
				writeJSON(w, 400, map[string]string{"error": "Choose an existing project."})
				return
			}
			if !slices.Contains(v.Projects, p) {
				v.Projects = append(v.Projects, p)
			}
		}
		if create || !req.KeepValue {
			v.Value, err = a.Deployer.Secrets.Seal(req.Value, deploy.SharedBinding(v.Name))
			if err != nil {
				a.fail(w, err)
				return
			}
		}
	}
	err := a.Store.SaveShared(r.Context(), v, create, remove)
	if errors.Is(err, store.ErrExists) {
		writeJSON(w, 409, map[string]string{"error": "A shared variable already uses this name."})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, 404, map[string]string{"error": "This variable no longer exists."})
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"saved": true})
}
