package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"pier/internal/requests"
	"pier/internal/telemetry"
)

// telemetryFail answers a failed query: the stack being off or a query
// that doesn't parse are the caller's to fix; anything else is ours.
func (a *API) telemetryFail(w http.ResponseWriter, err error) {
	var qe telemetry.QueryError
	switch {
	case errors.Is(err, telemetry.ErrOff):
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "off": true})
	case errors.Is(err, telemetry.ErrNoTrace):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.As(err, &qe):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": qe.Msg})
	default:
		a.Log.Warn("telemetry query", "err", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
	}
}

// TraceRow is a trace in the list, with the request that started it when
// Pier's router did.
type TraceRow struct {
	telemetry.TraceSummary
	Status     int    `json:"status,omitempty"`
	Deployment string `json:"deployment,omitempty"`
}

// projectTraces is GET /api/projects/{project}/traces?q=&from=&to=&env=:
// the project's traces that match a TraceQL query.
func (a *API) projectTraces(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	if _, ok := a.findProject(w, r, name); !ok {
		return
	}
	if a.Telemetry == nil || !a.Telemetry.On(r.Context()) {
		writeJSON(w, http.StatusOK, map[string]any{"on": false, "traces": []TraceRow{}})
		return
	}
	env, ok := envOf(w, r)
	if !ok {
		return
	}
	from, to := traceSpan(r)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if env != "" {
		filter := `{ resource.deployment.environment = "` + env + `" }`
		if q == "" || q == "{}" {
			q = filter
		} else if !strings.Contains(q, "|") {
			q = filter + " && " + q
		}
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	found, err := a.Telemetry.Search(r.Context(), name, q, time.UnixMilli(from), time.UnixMilli(to), limit)
	if err != nil {
		a.telemetryFail(w, err)
		return
	}
	rows := make([]TraceRow, len(found))
	ids := make([]string, len(found))
	for i, t := range found {
		rows[i] = TraceRow{TraceSummary: t}
		ids[i] = t.ID
	}
	if a.Requests != nil && len(ids) > 0 {
		// Caddy logs a request when it ends: up to a minute after its trace began.
		lines, err := a.Requests.ByTrace(r.Context(), name, ids, from, to+time.Minute.Milliseconds())
		if err != nil {
			a.fail(w, err)
			return
		}
		for i := range rows {
			if l, ok := lines[rows[i].ID]; ok {
				rows[i].Status, rows[i].Deployment = l.Status, l.Deployment
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"on": true, "traces": rows, "from": from, "to": to, "limit": limit})
}

// traceSpan is the span asked for, within the 14 days Tempo keeps.
func traceSpan(r *http.Request) (int64, int64) {
	from, to := span(r)
	if q := r.URL.Query(); q.Get("from") == "" {
		from = to - time.Hour.Milliseconds()
	}
	if oldest := time.Now().Add(-telemetry.Retention).UnixMilli(); from < oldest {
		from = oldest
	}
	return from, max(from, to)
}

// projectTrace is GET /api/projects/{project}/traces/{id}: the whole
// trace, the request that started it, and what was logged with it.
func (a *API) projectTrace(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	if _, ok := a.findProject(w, r, name); !ok {
		return
	}
	if a.Telemetry == nil {
		a.telemetryFail(w, telemetry.ErrOff)
		return
	}
	t, err := a.Telemetry.Trace(r.Context(), name, r.PathValue("id"))
	if err != nil {
		a.telemetryFail(w, err)
		return
	}
	start := time.UnixMicro(int64(t.Start * 1000))
	end := start.Add(time.Duration(t.MS * float64(time.Millisecond)))
	var request *requests.Line
	if a.Requests != nil {
		lines, err := a.Requests.ByTrace(r.Context(), name, []string{t.ID}, start.Add(-time.Second).UnixMilli(), end.Add(time.Minute).UnixMilli())
		if err != nil {
			a.fail(w, err)
			return
		}
		if l, ok := lines[t.ID]; ok {
			request = &l
		}
	}
	// An app may log a little before its spans start or after they end.
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	logs, err := a.Telemetry.TraceLogs(ctx, name, t.ID, start.Add(-time.Minute), end.Add(5*time.Minute))
	if err != nil {
		a.Log.Debug("trace logs", "err", err)
		logs = nil
	}
	if logs == nil {
		logs = []telemetry.LogLine{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"trace": t, "request": request, "logs": logs})
}

// projectServiceMap is GET /api/projects/{project}/servicemap?from=&to=&env=.
func (a *API) projectServiceMap(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	if _, ok := a.findProject(w, r, name); !ok {
		return
	}
	if a.Telemetry == nil || !a.Telemetry.On(r.Context()) {
		writeJSON(w, http.StatusOK, map[string]any{"on": false, "nodes": []telemetry.MapNode{}, "edges": []telemetry.MapEdge{}})
		return
	}
	env, ok := envOf(w, r)
	if !ok {
		return
	}
	from, to := traceSpan(r)
	nodes, edges, err := a.Telemetry.ServiceMap(r.Context(), name, env, time.UnixMilli(from), time.UnixMilli(to))
	if err != nil {
		a.telemetryFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"on": true, "nodes": nodes, "edges": edges, "from": from, "to": to})
}
