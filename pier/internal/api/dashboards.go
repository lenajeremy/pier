package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"pier/internal/store"
)

// A project's dashboards: pages of panels its members put together in
// Analytics. pierd keeps each one's spec as the dashboard wrote it.

type dashboardBody struct {
	Name string          `json:"name"`
	Spec json.RawMessage `json:"spec"`
}

func readDashboard(w http.ResponseWriter, r *http.Request) (dashboardBody, bool) {
	var b dashboardBody
	if err := json.NewDecoder(io.LimitReader(r.Body, 512<<10)).Decode(&b); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "send the dashboard as JSON, under 512 KB"})
		return b, false
	}
	b.Name = strings.TrimSpace(b.Name)
	if b.Name == "" || len([]rune(b.Name)) > 80 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "a dashboard's name is 1 to 80 characters"})
		return b, false
	}
	if len(b.Spec) == 0 {
		b.Spec = json.RawMessage(`{"panels":[]}`)
	}
	var spec struct {
		Panels []json.RawMessage `json:"panels"`
	}
	if err := json.Unmarshal(b.Spec, &spec); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "the spec is a JSON object with a list of panels"})
		return b, false
	}
	if len(spec.Panels) > 60 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "a dashboard has up to 60 panels"})
		return b, false
	}
	return b, true
}

func dashboardJSON(d store.Dashboard) map[string]any {
	return map[string]any{"id": d.ID, "name": d.Name, "spec": json.RawMessage(d.Spec), "updated_at": d.UpdatedAt, "updated_by": d.UpdatedBy}
}

func (a *API) dashboardFail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNoDashboard):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, store.ErrDashboardName):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	default:
		a.fail(w, err)
	}
}

func dashboardID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": store.ErrNoDashboard.Error()})
		return 0, false
	}
	return id, true
}

// projectDashboards lists a project's dashboards.
func (a *API) projectDashboards(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	if _, ok := a.findProject(w, r, name); !ok {
		return
	}
	list, err := a.Store.Dashboards(r.Context(), name)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"dashboards": list})
}

func (a *API) projectDashboard(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	if _, ok := a.findProject(w, r, name); !ok {
		return
	}
	id, ok := dashboardID(w, r)
	if !ok {
		return
	}
	d, err := a.Store.DashboardByID(r.Context(), name, id)
	if err != nil {
		a.dashboardFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dashboardJSON(d))
}

func (a *API) createDashboard(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	if _, ok := a.findProject(w, r, name); !ok {
		return
	}
	b, ok := readDashboard(w, r)
	if !ok {
		return
	}
	d, err := a.Store.SaveDashboard(r.Context(), store.Dashboard{Project: name, Name: b.Name, Spec: string(b.Spec), UpdatedBy: userFrom(r.Context()).DisplayName()})
	if err != nil {
		a.dashboardFail(w, err)
		return
	}
	a.event(r.Context(), name, "Made the dashboard "+d.Name, "", "")
	writeJSON(w, http.StatusCreated, dashboardJSON(d))
}

func (a *API) saveDashboard(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	if _, ok := a.findProject(w, r, name); !ok {
		return
	}
	id, ok := dashboardID(w, r)
	if !ok {
		return
	}
	b, ok := readDashboard(w, r)
	if !ok {
		return
	}
	d, err := a.Store.SaveDashboard(r.Context(), store.Dashboard{ID: id, Project: name, Name: b.Name, Spec: string(b.Spec), UpdatedBy: userFrom(r.Context()).DisplayName()})
	if err != nil {
		a.dashboardFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dashboardJSON(d))
}

func (a *API) deleteDashboard(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	if _, ok := a.findProject(w, r, name); !ok {
		return
	}
	id, ok := dashboardID(w, r)
	if !ok {
		return
	}
	d, err := a.Store.DashboardByID(r.Context(), name, id)
	if err != nil {
		a.dashboardFail(w, err)
		return
	}
	if err := a.Store.DeleteDashboard(r.Context(), name, id); err != nil {
		a.dashboardFail(w, err)
		return
	}
	a.event(r.Context(), name, "Deleted the dashboard "+d.Name, "", "")
	w.WriteHeader(http.StatusNoContent)
}
