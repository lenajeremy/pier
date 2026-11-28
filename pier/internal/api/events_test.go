package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pier/internal/requests"
	"pier/internal/store"
)

func TestServerEvents(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "pier.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	reqs, err := requests.Open(filepath.Join(t.TempDir(), "requests.db"), func(string) string { return "" }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer reqs.Close()
	a := &API{Store: st, Requests: reqs, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	srv := httptest.NewServer(a.Public())
	defer srv.Close()
	token, err := EventsToken(ctx, st, "shop", "production")
	if err != nil || !strings.HasPrefix(token, "pier_ev_shop.production.") {
		t.Fatalf("token %q %v", token, err)
	}
	again, _ := EventsToken(ctx, st, "shop", "production")
	if again != token {
		t.Fatal("a project's token changed")
	}
	send := func(token, body string) int {
		req, _ := http.NewRequest("POST", srv.URL+"/api/events", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if c := send(token, `[{"name":"order","value":42.5,"props":{"plan":"pro"},"user":"u1"},{"name":"signup","user":"u2"}]`); c != http.StatusAccepted {
		t.Fatalf("send: %d", c)
	}
	if c := send(token+"x", `{"name":"order"}`); c != http.StatusUnauthorized {
		t.Fatalf("a changed token: %d", c)
	}
	forged := strings.Replace(token, "pier_ev_shop.", "pier_ev_blog.", 1)
	if c := send(forged, `{"name":"order"}`); c != http.StatusUnauthorized {
		t.Fatalf("shop's signature for another project: %d", c)
	}
	if c := send(token, `{"name":""}`); c != http.StatusBadRequest {
		t.Fatalf("an event without a name: %d", c)
	}
	if err := reqs.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := reqs.Events(ctx, "shop", "production", time.Now().Add(-time.Minute).UnixMilli(), time.Now().Add(time.Minute).UnixMilli())
	if err != nil || len(rows) != 2 || rows[0].Visitors != 1 {
		t.Fatalf("stored %+v %v", rows, err)
	}
}
