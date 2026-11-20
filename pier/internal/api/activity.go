package api

import (
	"context"
	"net/http"
	"strconv"
)

func (a *API) activity(w http.ResponseWriter, r *http.Request) {
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	es, err := a.Store.Events(r.Context(), r.URL.Query().Get("project"), before)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"events": es})
}
func (a *API) event(ctx context.Context, project, title, detail, id string) {
	if err := a.Store.RecordEvent(ctx, project, title, detail, id); err != nil {
		a.Log.Warn("record activity", "err", err)
	}
}
