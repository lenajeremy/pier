package requests

import (
	"context"
	"io"
	"log/slog"
	"maps"
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
	buckets, step, err := l.Chart(ctx, "blog", now-24*hour, now, "", "", 0)
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
	minute, step, _ := l.Chart(ctx, "blog", now-time.Hour.Milliseconds(), now, "", "", 0)
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
		buckets, _, err := l.Chart(ctx, "blog", start.UnixMilli(), end.UnixMilli(), tc.status, "", 0)
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
	buckets, _, err := l.Chart(ctx, "blog", at.UnixMilli(), at.Add(24*time.Hour).UnixMilli(), "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 24 || buckets[0].Requests != 1 || buckets[0].E5xx != 1 {
		t.Fatalf("expected one retained server error, got %v", buckets)
	}
}

func TestOffset(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"": 0, "0": 0, "-480": -8 * time.Hour, "330": 5*time.Hour + 30*time.Minute, "840": 14 * time.Hour, "-840": -14 * time.Hour,
	} {
		if got, err := Offset(in); err != nil || got != want {
			t.Errorf("Offset(%q) = %v, %v, want %v", in, got, err, want)
		}
	}
	// 2^53 minutes overflows to nothing as a duration; it still isn't an offset.
	for _, in := range []string{"841", "-841", "5.5", "330m", " 60", "UTC", "9007199254740992"} {
		if got, err := Offset(in); err == nil {
			t.Errorf("Offset(%q) = %v, want an error", in, got)
		}
	}
}

// today is the midnight that started today for a viewer tz east of UTC.
func today(tz time.Duration) time.Time {
	zone := time.FixedZone("", int(tz/time.Second))
	now := time.Now().In(zone)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, zone)
}

// A chart of days is cut at the viewer's midnights: west of UTC, east of it,
// and where midnight falls inside a UTC hour. The first and the last
// millisecond of a day both count for that day, whether the chart reads the
// hourly counts (no filter) or the lines (a filter).
func TestChartDaysAreTheViewers(t *testing.T) {
	ctx := context.Background()
	day := 24 * time.Hour
	for _, tz := range []time.Duration{-8 * time.Hour, 9 * time.Hour, 5*time.Hour + 30*time.Minute} {
		l := open(t)
		to := today(tz)
		from := to.Add(-10 * day)
		busy := map[int]bool{0: true, 4: true, 9: true}
		l.Add(
			Line{Project: "blog", At: from.Add(-time.Millisecond).UnixMilli(), Status: 200},
			Line{Project: "blog", At: to.UnixMilli(), Status: 200},
		)
		for d := range busy {
			start := from.Add(time.Duration(d) * day)
			l.Add(
				Line{Project: "blog", At: start.UnixMilli(), Status: 404},
				Line{Project: "blog", At: start.Add(day - time.Millisecond).UnixMilli(), Status: 500},
			)
		}
		if err := l.Flush(ctx); err != nil {
			t.Fatal(err)
		}
		for _, status := range []string{"", "errors"} {
			buckets, step, err := l.Chart(ctx, "blog", from.UnixMilli(), to.UnixMilli(), status, "", tz)
			if err != nil || step != day.Milliseconds() || len(buckets) != 10 {
				t.Fatalf("%v from UTC, status %q: %d buckets of %dms, %v", tz, status, len(buckets), step, err)
			}
			for i, got := range buckets {
				want := Bucket{At: from.Add(time.Duration(i) * day).UnixMilli()}
				if busy[i] {
					want.Requests, want.Errors, want.E4xx, want.E5xx = 2, 2, 1, 1
				}
				if got != want {
					t.Errorf("%v from UTC, status %q, day %d: got %+v, want %+v", tz, status, i, got, want)
				}
			}
		}
	}
}

// Once the lines are gone, the hour that holds a half-hour zone's midnight
// can't be split any more: all of it counts for the day that holds the
// hour's middle. The days the lines still cover stay exact.
func TestChartHalfHourZoneAfterLineRetention(t *testing.T) {
	ctx := context.Background()
	l := open(t)
	tz := 5*time.Hour + 30*time.Minute
	to := today(tz)
	from, old, recent := to.AddDate(0, 0, -30), to.AddDate(0, 0, -20), to.AddDate(0, 0, -3)
	for _, midnight := range []time.Time{old, recent} {
		l.Add(
			Line{Project: "blog", At: midnight.Add(-10 * time.Minute).UnixMilli(), Status: 200},
			Line{Project: "blog", At: midnight.Add(10 * time.Minute).UnixMilli(), Status: 200},
		)
	}
	if err := l.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	l.cleanUp(ctx)
	buckets, _, err := l.Chart(ctx, "blog", from.UnixMilli(), to.UnixMilli(), "", "", tz)
	if err != nil || len(buckets) != 30 {
		t.Fatalf("%d buckets, %v", len(buckets), err)
	}
	got := map[int]int{}
	for i, b := range buckets {
		if b.Requests > 0 {
			got[i] = b.Requests
		}
	}
	// Day 10 starts at the old midnight, day 27 at the recent one.
	if want := map[int]int{10: 2, 26: 1, 27: 1}; !maps.Equal(got, want) {
		t.Fatalf("requests by day: got %v, want %v", got, want)
	}
}

func TestFollowReadsEveryMatchingLineAcrossPages(t *testing.T) {
	ctx := context.Background()
	l := open(t)
	now := time.Now().UnixMilli()
	for i := 0; i < 625; i++ {
		l.Add(Line{Project: "docs", At: now, Path: "/missing", Method: "GET", Status: 404})
	}
	if err := l.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	f := Filter{From: now - 1, To: now + 1, Status: "404", Query: "missing", Forward: true, Limit: 500}
	for _, want := range []int{500, 125, 0} {
		rows, err := l.Lines(ctx, "docs", f)
		if err != nil || len(rows) != want {
			t.Fatalf("after %d: got %d lines, %v", f.After, len(rows), err)
		}
		for _, r := range rows {
			if r.ID <= f.After {
				t.Fatalf("out-of-order ID %d after %d", r.ID, f.After)
			}
			f.After = r.ID
		}
	}
}
