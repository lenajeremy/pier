package api

import (
	"net/http"
	"time"

	"pier/internal/requests"
)

// analyticsOverview is the Analytics tab's Overview: visitors, pages,
// where they came from, errors and response times, read from the request
// log. ?env= is production or preview; without it, both.
func (a *API) analyticsOverview(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	if _, ok := a.findProject(w, r, name); !ok {
		return
	}
	env := r.URL.Query().Get("env")
	if env != "" && env != "production" && env != "preview" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "env is production or preview"})
		return
	}
	from, to := span(r)
	// The breakdowns need each request, and those are kept 14 days.
	if oldest := time.Now().Add(-requests.KeepLines).UnixMilli(); from < oldest {
		from = oldest
	}
	if a.Requests == nil {
		writeJSON(w, http.StatusOK, requests.Overview{From: from, To: to})
		return
	}
	o, err := a.Requests.Overview(r.Context(), name, env, from, to, 5)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, o)
}
