package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

// addEvents writes n events a millisecond apart, the last one newest, for
// each project in turn, and returns when the first one happened.
func addEvents(t *testing.T, s *Store, n int, projects ...string) int64 {
	t.Helper()
	base := time.Now().Add(-time.Hour).UnixMilli()
	for i := 0; i < n; i++ {
		if _, err := s.db.Exec("INSERT INTO activity(at,project,title) VALUES(?,?,?)", base+int64(i), projects[i%len(projects)], "Changed variable"); err != nil {
			t.Fatal(err)
		}
	}
	return base
}

// pages reads a project's activity a page at a time, as the dashboard does.
func pages(t *testing.T, s *Store, project string) [][]Event {
	t.Helper()
	var out [][]Event
	var before int64
	for {
		page, err := s.Events(context.Background(), project, before)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			return out
		}
		out = append(out, page)
		before = page[len(page)-1].ID
	}
}

func TestEventsNewestFirstByProjectAndPage(t *testing.T) {
	s := open(t)
	base := addEvents(t, s, 250, "blog", "docs", "")
	for _, tc := range []struct {
		project string
		sizes   []int
	}{{"", []int{100, 100, 50}}, {"blog", []int{84}}, {"docs", []int{83}}, {"gone", nil}} {
		got := pages(t, s, tc.project)
		if len(got) != len(tc.sizes) {
			t.Fatalf("%q: %d pages, want %d", tc.project, len(got), len(tc.sizes))
		}
		last := base + 250
		for i, page := range got {
			if len(page) != tc.sizes[i] {
				t.Fatalf("%q: page %d has %d events, want %d", tc.project, i, len(page), tc.sizes[i])
			}
			for _, e := range page {
				if e.At >= last || (tc.project != "" && e.Project != tc.project) {
					t.Fatalf("%q: event %+v after one at %d: out of order, repeated or another project's", tc.project, e, last)
				}
				last = e.At
			}
		}
	}
	if page, err := s.Events(context.Background(), "", 1<<40); err != nil || len(page) != 0 {
		t.Fatalf("a page after an event that isn't there: %v, %v", page, err)
	}
}

// Each kind of page comes off its index in order, without a sort and
// without walking other projects' rows: the Activity page asks every 5 s.
func TestEventsReadAnIndexInOrder(t *testing.T) {
	s := open(t)
	addEvents(t, s, 50, "blog", "docs")
	for _, tc := range []struct {
		project string
		before  int64
		index   string
	}{{"", 0, "activity_time"}, {"", 40, "activity_time"}, {"blog", 0, "activity_by_project"}, {"blog", 40, "activity_by_project"}} {
		q, args := eventsQuery(tc.project, tc.before)
		rows, err := s.db.Query("EXPLAIN QUERY PLAN "+q, args...)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		rows.Close()
		steps := strings.Join(plan, "\n")
		if !strings.Contains(steps, "activity USING INDEX "+tc.index) || strings.Contains(steps, "TEMP B-TREE") {
			t.Errorf("project %q, before %d: want %s walked in order, got:\n%s", tc.project, tc.before, tc.index, steps)
		}
	}
}

func TestPruneEvents(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	now := time.Now()
	for title, at := range map[string]time.Time{
		"over a year ago":  now.Add(-KeepEvents - time.Hour),
		"under a year ago": now.Add(-KeepEvents + time.Hour),
		"just now":         now,
	} {
		if _, err := s.db.Exec("INSERT INTO activity(at,project,title) VALUES(?,?,?)", at.UnixMilli(), "blog", title); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := s.PruneEvents(ctx); n != 1 || err != nil {
		t.Fatalf("pruned %d events, %v, want 1", n, err)
	}
	left, err := s.Events(ctx, "", 0)
	if err != nil || len(left) != 2 || left[0].Title != "just now" || left[1].Title != "under a year ago" {
		t.Fatalf("left %+v, %v", left, err)
	}
	if n, err := s.PruneEvents(ctx); n != 0 || err != nil {
		t.Fatalf("pruned %d events the second time, %v", n, err)
	}
}
