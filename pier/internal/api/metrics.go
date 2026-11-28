package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"pier/internal/telemetry"
)

// projectMetrics is GET /api/projects/{project}/metrics: the metric
// families the project's apps sent in the last day.
func (a *API) projectMetrics(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	if _, ok := a.findProject(w, r, name); !ok {
		return
	}
	if a.Telemetry == nil || !a.Telemetry.On(r.Context()) {
		writeJSON(w, http.StatusOK, map[string]any{"on": false, "metrics": []telemetry.Metric{}})
		return
	}
	list, err := a.Telemetry.Metrics(r.Context(), name)
	if err != nil {
		a.telemetryFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"on": true, "metrics": list})
}

// projectMetricLabels is GET /api/projects/{project}/metrics/labels?metric=:
// a metric's labels and their values.
func (a *API) projectMetricLabels(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	if _, ok := a.findProject(w, r, name); !ok {
		return
	}
	metric := r.URL.Query().Get("metric")
	if metric == "" || strings.ContainsAny(metric, "{}\"") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name a metric"})
		return
	}
	if a.Telemetry == nil {
		a.telemetryFail(w, telemetry.ErrOff)
		return
	}
	labels, err := a.Telemetry.Labels(r.Context(), name, metric)
	if err != nil {
		a.telemetryFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"labels": labels})
}

// projectMetricQuery is GET /api/projects/{project}/metrics/query?q=&from=&to=&env=:
// PromQL over the project's own series.
func (a *API) projectMetricQuery(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	if _, ok := a.findProject(w, r, name); !ok {
		return
	}
	env, ok := envOf(w, r)
	if !ok {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" || len(q) > 8192 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "write a PromQL query"})
		return
	}
	if a.Telemetry == nil {
		a.telemetryFail(w, telemetry.ErrOff)
		return
	}
	from, to := traceSpan(r)
	res, err := a.Telemetry.Query(r.Context(), name, env, q, time.UnixMilli(from), time.UnixMilli(to), stepOf(r))
	if err != nil {
		a.telemetryFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": res, "from": from, "to": to})
}

// stepOf is the ?step= a chart asked for, in seconds; 0 lets the server pick.
func stepOf(r *http.Request) time.Duration {
	n, err := strconv.Atoi(r.URL.Query().Get("step"))
	if err != nil || n <= 0 || n > 86400 {
		return 0
	}
	return time.Duration(n) * time.Second
}
