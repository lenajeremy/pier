package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"pier/internal/apphost"
)

func TestLiveViewKeepsPausedTextAndWraps(t *testing.T) {
	m := logView{source: logSource{project: "site", kind: "Runtime"}, width: 45, height: 16}
	update := func(msg tea.Msg) { v, _ := m.Update(msg); m = v.(logView) }
	entry := runtimeRow(apphost.LogLine{At: "2026-11-21T01:02:03Z", Stream: "stderr", Text: "\x1b[31ma message long enough to wrap onto another line\x1b[0m"})
	update(logBatch{rows: []logRow{entry}})
	update(tea.KeyMsg{Type: tea.KeySpace})
	update(logBatch{rows: []logRow{runtimeRow(apphost.LogLine{Text: "next entry"})}})
	view := m.View()
	if strings.Contains(view, "next entry") || !strings.Contains(view, "1 new") || !strings.Contains(view, "stderr") || strings.Contains(view, "\x1b") {
		t.Fatal(view)
	}
	for _, line := range strings.Split(view, "\n") {
		if ansi.StringWidth(line) > m.width {
			t.Fatalf("overflow: %q", line)
		}
	}
	update(tea.KeyMsg{Type: tea.KeySpace})
	if !strings.Contains(m.View(), "next entry") {
		t.Fatal(m.View())
	}
}

func TestRuntimeFollowWithSnapshotOnlyServer(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture" {
			t.Error("missing authentication")
		}
		calls++
		const l = `{"at":"2026-11-21T01:02:03Z","stream":"stdout","text":"same"}`
		if calls == 1 {
			fmt.Fprint(w, `{"lines":[`+l+`]}`)
			return
		}
		if r.URL.Query().Get("since") == "" {
			t.Error("missing follow cursor")
		}
		fmt.Fprint(w, `{"lines":[`+l+","+l+`]}`)
	}))
	defer srv.Close()
	s := logSource{c: config{Server: srv.URL, Token: "fixture"}, kind: "Runtime", project: "site", deployment: "abc", limit: 1, follow: true}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	stop := errors.New("received both")
	total := 0
	err := s.read(ctx, func(batch []logRow) error {
		total += len(batch)
		if total >= 2 {
			return stop
		}
		return nil
	})
	if err != stop || total != 2 {
		t.Fatalf("received %d lines, err %v", total, err)
	}
}
