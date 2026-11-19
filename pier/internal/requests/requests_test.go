package requests

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"
)

func open(t *testing.T) *Log {
	t.Helper()
	l, err := Open(filepath.Join(t.TempDir(), "requests.db"), func(dep string) string {
		if dep == "c19e77" {
			return "blog"
		}
		return ""
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}

func TestParse(t *testing.T) {
	l := open(t)
	line, ok := l.parse([]byte(`{"ts":1795081440.245,"request":{"method":"GET","uri":"/search?q=caddy","remote_ip":"203.0.113.9"},"duration":0.012,"size":512,"status":200,"resp_headers":{"X-Pier-Deployment":["c19e77"]}}`))
	if !ok || line.Project != "blog" || line.Path != "/search?q=caddy" || line.Status != 200 || line.MS < 11.9 || line.At != 1795081440245 {
		t.Fatalf("got %+v, %v", line, ok)
	}
	if _, ok := l.parse([]byte(`{"ts":1,"request":{"uri":"/api/session"},"status":200,"resp_headers":{}}`)); ok {
		t.Fatal("a request no deployment answered (the dashboard's) was kept")
	}
	if _, ok := l.parse([]byte(`{"ts":1,"status":200,"resp_headers":{"X-Pier-Deployment":["gone00"]}}`)); ok {
		t.Fatal("a request for an unknown deployment was kept")
	}
}

func TestChartAndLines(t *testing.T) {
	ctx := context.Background()
	l := open(t)
	now := time.Now().UnixMilli()
	hour := time.Hour.Milliseconds()
	l.Add(
		Line{Project: "blog", Deployment: "c19e77", At: now - 3*hour, Method: "GET", Path: "/", Status: 200},
		Line{Project: "blog", Deployment: "c19e77", At: now - 3*hour + 1000, Method: "GET", Path: "/.env", Status: 404},
		Line{Project: "blog", Deployment: "c19e77", At: now - 1000, Method: "GET", Path: "/about", Status: 200},
		Line{Project: "docs", Deployment: "aaaaaa", At: now - 1000, Method: "GET", Path: "/", Status: 500},
	)
	if err := l.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	buckets, step, err := l.Chart(ctx, "blog", now-24*hour, now, "", "")
	if err != nil || step != hour {
		t.Fatalf("step %d, %v", step, err)
	}
	reqs, errs := 0, 0
	for _, b := range buckets {
		reqs += b.Requests
		errs += b.Errors
	}
	if reqs != 3 || errs != 1 {
		t.Fatalf("24h chart counted %d requests and %d errors, want 3 and 1", reqs, errs)
	}
	minute, step, _ := l.Chart(ctx, "blog", now-time.Hour.Milliseconds(), now, "", "")
	if step != time.Minute.Milliseconds() || len(minute) < 60 {
		t.Fatalf("a 1-hour chart should have minute buckets, got %d of %dms", len(minute), step)
	}
	errors, err := l.Lines(ctx, "blog", Filter{From: now - 24*hour, Status: "errors"})
	if err != nil || len(errors) != 1 || errors[0].Path != "/.env" {
		t.Fatalf("errors filter: %+v, %v", errors, err)
	}
	all, _ := l.Lines(ctx, "blog", Filter{From: now - 24*hour})
	if len(all) != 3 || all[0].Path != "/" || all[2].Path != "/about" {
		t.Fatalf("lines should read oldest first: %+v", all)
	}
	newer, _ := l.Lines(ctx, "blog", Filter{From: now - 24*hour, After: all[1].ID})
	if len(newer) != 1 || newer[0].Path != "/about" {
		t.Fatalf("after: %+v", newer)
	}
	if found, _ := l.Lines(ctx, "blog", Filter{From: now - 24*hour, Query: "ABOUT"}); len(found) != 1 {
		t.Fatalf("path search should ignore case: %+v", found)
	}
}

// Midnight in Kolkata falls halfway through a UTC hour. Both that partial
// hour and the one at the end must agree with the individual request lines.
func TestChartLocalCalendarDay(t *testing.T) {
	ctx := context.Background()
	l := open(t)
	zone := time.FixedZone("IST", 5*3600+1800)
	now := time.Now().In(zone)
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, zone)
	start := end.AddDate(0, 0, -1)
	for _, r := range []struct {
		at     time.Time
		status int
	}{
		{start.Add(-time.Millisecond), 500},
		{start, 404},
		{start.Add(12 * time.Hour), 200},
		{end.Add(-time.Millisecond), 502},
		{end, 404},
	} {
		l.Add(Line{Project: "blog", At: r.at.UnixMilli(), Status: r.status})
	}
	if err := l.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		status string
		count  int
	}{{"", 3}, {"errors", 2}} {
		buckets, _, err := l.Chart(ctx, "blog", start.UnixMilli(), end.UnixMilli(), tc.status, "")
		if err != nil {
			t.Fatal(err)
		}
		var total Bucket
		for _, b := range buckets {
			total.Requests += b.Requests
			total.Errors += b.Errors
			total.E4xx += b.E4xx
			total.E5xx += b.E5xx
		}
		if total.Requests != tc.count || total.Errors != 2 || total.E4xx != 1 || total.E5xx != 1 {
			t.Errorf("status %q: got %+v", tc.status, total)
		}
	}
}

func TestHistoricalChartAfterLineRetention(t *testing.T) {
	ctx := context.Background()
	l := open(t)
	at := time.Now().UTC().AddDate(0, -3, 0).Truncate(time.Hour)
	l.Add(Line{Project: "blog", At: at.UnixMilli(), Status: 500})
	if err := l.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	l.cleanUp(ctx)
	lines, err := l.Lines(ctx, "blog", Filter{From: at.UnixMilli()})
	if err != nil || len(lines) != 0 {
		t.Fatalf("expired request lines: %v, %v", lines, err)
	}
	buckets, _, err := l.Chart(ctx, "blog", at.UnixMilli(), at.Add(24*time.Hour).UnixMilli(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 24 || buckets[0].Requests != 1 || buckets[0].E5xx != 1 {
		t.Fatalf("expected one retained server error, got %v", buckets)
	}
}
