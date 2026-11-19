package api

import (
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
	return ""
}

// requestLines is the Logs tab's list: the newest requests in the range,
// or with ?after= only those newer than the last one seen, for following.
func (a *API) requestLines(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	if _, ok := a.findProject(w, r, name); !ok {
		return
	}
	if a.Requests == nil {
		writeJSON(w, http.StatusOK, map[string]any{"lines": []requests.Line{}, "kept_days": 14})
		return
	}
	q := r.URL.Query()
	from, to := span(r)
	after, _ := strconv.ParseInt(q.Get("after"), 10, 64)
	lines, err := a.Requests.Lines(r.Context(), name, requests.Filter{
		From: from, To: to, Status: statusFilter(q.Get("status")), Query: q.Get("q"), After: after, Limit: 300,
	})
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
func (a *API) requestChart(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	if _, ok := a.findProject(w, r, name); !ok {
		return
	}
	from, to := span(r)
	q := r.URL.Query()
	var buckets []requests.Bucket
	var step int64
	if a.Requests != nil {
		var err error
		buckets, step, err = a.Requests.Chart(r.Context(), name, from, to, statusFilter(q.Get("status")), q.Get("q"))
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
