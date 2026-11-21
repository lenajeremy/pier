package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"pier/internal/requests"
)

// span reads ?from= and ?to= (ms); without them it's the last 24 hours. A
// range can go back 6 months, as far as the hourly counts are kept.
func span(r *http.Request) (from, to int64) {
	q := r.URL.Query()
	now := time.Now().UnixMilli()
	to, _ = strconv.ParseInt(q.Get("to"), 10, 64)
	if to <= 0 || to > now {
		to = now
	}
	from, _ = strconv.ParseInt(q.Get("from"), 10, 64)
	oldest := now - requests.KeepHours.Milliseconds()
	if from <= 0 || from >= to {
		from = to - (24 * time.Hour).Milliseconds()
	}
	if from < oldest {
		from = oldest
	}
	return from, max(from, to)
}

func statusFilter(s string) string {
	switch s {
	case "2xx", "3xx", "4xx", "5xx", "errors":
		return s
	}
	if n, err := strconv.Atoi(s); err == nil && n >= 100 && n <= 599 {
		return s
	}
	return ""
}

// requestLines is the Logs tab's list: the newest requests in the range,
// or with ?after= only those newer than the last one seen, for following.
func (a *API) requestLines(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	if _, ok := a.findProject(w, r, name); !ok {
		return
	}
	if a.Requests == nil && r.URL.Query().Get("follow") != "1" {
		writeJSON(w, http.StatusOK, map[string]any{"lines": []requests.Line{}, "kept_days": 14})
		return
	}
	q := r.URL.Query()
	from, to := span(r)
	after, _ := strconv.ParseInt(q.Get("after"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit < 1 || limit > 500 {
		limit = 300
	}
	filter := requests.Filter{
		From: from, To: to, Status: statusFilter(q.Get("status")), Query: q.Get("q"), After: after, Forward: q.Has("after"), Limit: limit,
	}
	if q.Get("follow") == "1" {
		a.followRequests(w, r, name, filter)
		return
	}
	lines, err := a.Requests.Lines(r.Context(), name, filter)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"lines": nonNil(lines), "kept_days": int(requests.KeepLines.Hours() / 24),
		"oldest_line": time.Now().Add(-requests.KeepLines).UnixMilli(),
	})
}

// requestChart is the Logs tab's chart: requests and errors over the range.
// ?zone= is the viewer's time zone, such as Europe/London, so a chart of
// days counts the days the viewer sees named, from one of their midnights to
// the next. ?tz= is how many minutes their clock is ahead of UTC, which
// older dashboards send and which stands in for a zone pierd doesn't know.
// Without either the days are UTC's.
func (a *API) requestChart(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	if _, ok := a.findProject(w, r, name); !ok {
		return
	}
	from, to := span(r)
	q := r.URL.Query()
	tz, err := requests.Zone(q.Get("zone"), q.Get("tz"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tz is " + err.Error()})
		return
	}
	var buckets []requests.Bucket
	var step int64
	if a.Requests != nil {
		buckets, step, err = a.Requests.Chart(r.Context(), name, from, to, statusFilter(q.Get("status")), q.Get("q"), tz)
		if err != nil {
			a.fail(w, err)
			return
		}
	}
	total, errs, e4, e5 := 0, 0, 0, 0
	var busiest requests.Bucket
	for _, b := range buckets {
		total += b.Requests
		errs += b.Errors
		e4 += b.E4xx
		e5 += b.E5xx
		if b.Requests > busiest.Requests {
			busiest = b
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"from": from, "to": to, "bucket_ms": step, "buckets": nonNil(buckets),
		"requests": total, "errors": errs, "busiest": busiest, "e4xx": e4, "e5xx": e5,
	})
}

// logStream writes newline-delimited JSON pages. An empty page flushes the
// headers even when a quiet project has no recent lines.
func logStream(w http.ResponseWriter) func(any) error {
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	encoder := json.NewEncoder(w)
	return func(page any) error {
		if err := encoder.Encode(page); err != nil {
			return err
		}
		return http.NewResponseController(w).Flush()
	}
}

func (a *API) followRequests(w http.ResponseWriter, r *http.Request, project string, filter requests.Filter) {
	send := logStream(w)
	if a.Requests == nil {
		if send(map[string]any{"lines": []requests.Line{}}) == nil {
			<-r.Context().Done()
		}
		return
	}
	// The initial snapshot and later pages share an ID cursor. Leave the
	// upper time bound open to include requests committed during startup.
	filter.To = 0
	first := true
	for {
		changed := a.Requests.Changed()
		lines, err := a.Requests.Lines(r.Context(), project, filter)
		if err != nil {
			_ = send(map[string]string{"error": err.Error()})
			return
		}
		if len(lines) > 0 || first {
			if send(map[string]any{"lines": nonNil(lines)}) != nil {
				return
			}
		}
		if len(lines) > 0 {
			filter.After = lines[len(lines)-1].ID
		}
		first = false
		full := len(lines) == filter.Limit
		filter.Forward, filter.Limit = true, 500
		if full {
			continue
		}
		select {
		case <-r.Context().Done():
			return
		case <-changed:
		}
	}
}
