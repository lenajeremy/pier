package requests

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"
)

func TestSentCheck(t *testing.T) {
	var s Sent
	json.Unmarshal([]byte(`{"name":"order","value":42.5,"props":{"plan":"pro","items":3,"gift":true,"none":null}}`), &s)
	e, err := s.Check()
	if err != nil || e.Name != "order" || *e.Value != 42.5 || e.Props["plan"] != "pro" || e.Props["items"] != "3" || e.Props["gift"] != "true" || len(e.Props) != 3 {
		t.Fatalf("%+v %v", e, err)
	}
	for _, bad := range []string{`{"name":""}`, `{"name":"has space"}`, `{"name":"x","value":"lots"}`, `{"name":"x","value":[1]}`, `{"name":"x","props":{"a":{"b":1}}}`} {
		var s Sent
		json.Unmarshal([]byte(bad), &s)
		if _, err := s.Check(); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestEventsAndFunnel(t *testing.T) {
	ctx := context.Background()
	l, err := Open(filepath.Join(t.TempDir(), "requests.db"), func(string) string { return "shop" }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	start := time.Now().Add(-time.Hour).UnixMilli()
	v := func(x float64) *float64 { return &x }
	// Three people visit; two start checkout; one orders on pro, and one
	// person who never visited orders on team through the server.
	salt := l.salts.get(time.UnixMilli(start))
	for i, ip := range []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"} {
		l.Add(Line{Project: "shop", Deployment: "d1", At: start + int64(i)*1000, Method: "GET", Path: "/", Status: 200, Page: true, Visitor: visitor(salt, ip, "ua"), Env: "production"})
	}
	l.AddEvent(Event{Project: "shop", Env: "production", At: start + 10_000, Name: "checkout_started"}, "10.0.0.1", "ua", "")
	l.AddEvent(Event{Project: "shop", Env: "production", At: start + 11_000, Name: "checkout_started"}, "10.0.0.2", "ua", "")
	l.AddEvent(Event{Project: "shop", Env: "production", At: start + 20_000, Name: "order", Value: v(40), Props: map[string]string{"plan": "pro"}}, "10.0.0.1", "ua", "")
	l.AddEvent(Event{Project: "shop", Env: "production", At: start + 21_000, Name: "order", Value: v(12.5), Props: map[string]string{"plan": "team"}}, "", "", "user-77")
	// An order before checkout started doesn't count as the next step.
	l.AddEvent(Event{Project: "shop", Env: "production", At: start + 5_000, Name: "order", Value: v(1), Props: map[string]string{"plan": "pro"}}, "10.0.0.2", "ua", "")
	if err := l.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	to := time.Now().UnixMilli()
	rows, err := l.Events(ctx, "shop", "", start, to)
	if err != nil || len(rows) != 2 || rows[0].Name != "order" || rows[0].Count != 3 || rows[0].Visitors != 3 || *rows[0].Value != 53.5 {
		t.Fatalf("events %+v %v", rows, err)
	}
	if rows[1].Value != nil {
		t.Fatalf("events without numbers have no value: %+v", rows[1])
	}
	d, err := l.Event(ctx, "shop", "", "order", "plan", start, to)
	if err != nil || d.Count != 3 || len(d.Breakdown) != 2 || d.Breakdown[0].Key != "pro" || d.Breakdown[0].Count != 2 || *d.Breakdown[1].Value != 12.5 || len(d.Props) != 1 || len(d.Series) != 2 {
		t.Fatalf("detail %+v %v", d, err)
	}
	var sum int64
	for _, c := range d.Series[0].Counts {
		sum += c
	}
	if sum != 2 {
		t.Fatalf("pro's series counts %d", sum)
	}
	counts, err := l.Funnel(ctx, "shop", "", []string{VisitStep, "checkout_started", "order"}, start, to)
	if err != nil || counts[0] != 3 || counts[1] != 2 || counts[2] != 1 {
		t.Fatalf("funnel %v %v", counts, err)
	}
	names, _ := l.EventNames(ctx, "shop", start, to)
	if len(names) != 2 {
		t.Fatalf("names %v", names)
	}
}
