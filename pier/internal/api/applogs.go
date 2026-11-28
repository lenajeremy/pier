package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"pier/internal/telemetry"
)

// The Logs view on Loki: what apps printed and every request, as one
// stream of lines. Loki keeps each project as its own tenant, so any
// LogQL reads only the project's lines.

func (a *API) logQuery(w http.ResponseWriter, r *http.Request) (project, q string, from, to time.Time, ok bool) {
	project = r.PathValue("project")
	if _, ok := a.findProject(w, r, project); !ok {
		return "", "", time.Time{}, time.Time{}, false
	}
	if a.Telemetry == nil || !a.Telemetry.On(r.Context()) {
		a.telemetryFail(w, telemetry.ErrOff)
		return "", "", time.Time{}, time.Time{}, false
	}
	q = strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		q = `{service_name=~".+"}`
	}
	if len(q) > 4096 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "that query is too long"})
		return "", "", time.Time{}, time.Time{}, false
	}
	f, t := traceSpan(r)
	from, to = time.UnixMilli(f), time.UnixMilli(t)
	// Page back from a line's exact time.
	if before, err := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64); err == nil && before > 0 {
		to = time.Unix(0, before-1)
	}
	return project, q, from, to, true
}

// projectLogLines is GET /api/projects/{project}/logs/lines?q=&from=&to=&before=.
func (a *API) projectLogLines(w http.ResponseWriter, r *http.Request) {
	project, q, from, to, ok := a.logQuery(w, r)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	lines, err := a.Telemetry.Lines(r.Context(), project, q, from, to, limit)
	if err != nil {
		a.telemetryFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines, "limit": limit})
}

// projectLogVolume is GET /api/projects/{project}/logs/volume: lines by level over time.
func (a *API) projectLogVolume(w http.ResponseWriter, r *http.Request) {
	project, q, from, to, ok := a.logQuery(w, r)
	if !ok {
		return
	}
	buckets, step, err := a.Telemetry.Volume(r.Context(), project, q, from, to)
	if err != nil {
		a.telemetryFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"buckets": buckets, "step": step})
}

// projectLogFields is GET /api/projects/{project}/logs/fields: what's in the lines.
func (a *API) projectLogFields(w http.ResponseWriter, r *http.Request) {
	project, q, from, to, ok := a.logQuery(w, r)
	if !ok {
		return
	}
	fields, err := a.Telemetry.Fields(r.Context(), project, q, from, to)
	if err != nil {
		a.telemetryFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"fields": fields})
}

// projectLogValues is GET /api/projects/{project}/logs/values?field=&parsed=:
// a field's commonest values.
func (a *API) projectLogValues(w http.ResponseWriter, r *http.Request) {
	project, q, from, to, ok := a.logQuery(w, r)
	if !ok {
		return
	}
	values, err := a.Telemetry.Values(r.Context(), project, q, r.URL.Query().Get("field"), r.URL.Query().Get("parsed"), from, to)
	if err != nil {
		a.telemetryFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"values": values})
}

// projectLogMetric is GET /api/projects/{project}/logs/metric?q=: a LogQL
// metric query, for charts.
func (a *API) projectLogMetric(w http.ResponseWriter, r *http.Request) {
	project, q, from, to, ok := a.logQuery(w, r)
	if !ok {
		return
	}
	res, err := a.Telemetry.LogMetric(r.Context(), project, q, from, to)
	if err != nil {
		a.telemetryFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": res, "from": from.UnixMilli(), "to": to.UnixMilli()})
}
