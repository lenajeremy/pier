package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"pier/internal/requests"
	"testing"
	"time"
)

func TestRequestStreamDrainsBurstAfterInitialTail(t *testing.T) {
	db, err := requests.Open(filepath.Join(t.TempDir(), "requests.db"), nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a := API{Requests: db}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.followRequests(w, r, "site", requests.Filter{Status: "errors", Limit: 1})
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	add := func(project string, status int) {
		db.Add(requests.Line{Project: project, Status: status, At: time.Now().UnixMilli()})
	}
	flush := func() {
		t.Helper()
		if err := db.Flush(ctx); err != nil {
			t.Fatal(err)
		}
	}
	add("site", 500)
	add("site", 404)
	flush()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	var cursor int64
	read := func(want int) {
		t.Helper()
		var p struct{ Lines []requests.Line }
		if err := dec.Decode(&p); err != nil {
			t.Fatal(err)
		}
		if len(p.Lines) != want {
			t.Fatalf("received %d lines, want %d", len(p.Lines), want)
		}
		for _, l := range p.Lines {
			if l.ID <= cursor || l.Status < 400 {
				t.Fatalf("unexpected request %+v", l)
			}
			cursor = l.ID
		}
	}
	read(1)
	add("another", 500)
	add("site", 200)
	for range 503 {
		add("site", 502)
	}
	flush()
	read(500)
	read(3)
}
