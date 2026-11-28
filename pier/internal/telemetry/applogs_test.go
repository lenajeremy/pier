package telemetry

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	coll "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/protobuf/proto"

	"pier/internal/store"
)

func TestFollowLogsSendsAppOutput(t *testing.T) {
	lookEvery, sendEvery = 20*time.Millisecond, 10*time.Millisecond
	defer func() { lookEvery, sendEvery = 10*time.Second, 2*time.Second }()
	var mu sync.Mutex
	var got []*coll.ExportLogsServiceRequest
	var tenants []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		req := &coll.ExportLogsServiceRequest{}
		if err := proto.Unmarshal(b, req); err != nil || r.URL.Path != "/otlp/v1/logs" {
			t.Errorf("%s: %v", r.URL.Path, err)
		}
		mu.Lock()
		got, tenants = append(got, req), append(tenants, r.Header.Get("X-Scope-OrgID"))
		mu.Unlock()
	}))
	defer srv.Close()
	st, err := store.Open(filepath.Join(t.TempDir(), "pier.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var sinces []string
	m := &Manager{Store: st, Containers: &fakeContainers{addr: strings.TrimPrefix(srv.URL, "http://")}, Dir: t.TempDir(), Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Apps: func(context.Context) ([]App, error) {
			return []App{{Container: "pier-app-d1", Project: "shop", Env: "production", Deployment: "d00001"}}, nil
		},
		OpenLogs: func(_ context.Context, _ string, since string) (io.ReadCloser, bool, error) {
			mu.Lock()
			sinces = append(sinces, since)
			n := len(sinces)
			mu.Unlock()
			if n > 1 {
				return io.NopCloser(strings.NewReader("")), true, nil
			}
			return io.NopCloser(strings.NewReader(
				"2026-11-28T10:00:00.000000001Z listening on 3000\n" +
					`2026-11-28T10:00:01.5Z {"level":"ERROR","msg":"card declined","trace_id":"5b8efff798038103d269b633813fc60c"}` + "\n")), true, nil
		},
	}
	if err := m.Enable(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.FollowLogs(ctx); close(done) }()
	time.Sleep(150 * time.Millisecond)
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if len(got) == 0 {
		t.Fatal("nothing sent")
	}
	var bodies []string
	for i, req := range got {
		if tenants[i] != "shop" {
			t.Fatalf("tenant %q", tenants[i])
		}
		for _, rl := range req.ResourceLogs {
			for _, sl := range rl.ScopeLogs {
				for _, r := range sl.LogRecords {
					bodies = append(bodies, r.Body.GetStringValue())
					if strings.Contains(r.Body.GetStringValue(), "declined") && (len(r.TraceId) != 16 || r.SeverityText != "error" || r.TimeUnixNano != uint64(time.Date(2026, 11, 28, 10, 0, 1, 5e8, time.UTC).UnixNano())) {
						t.Fatalf("the JSON line: trace %x level %q time %d", r.TraceId, r.SeverityText, r.TimeUnixNano)
					}
				}
			}
		}
	}
	if len(bodies) != 2 || bodies[0] != "listening on 3000" {
		t.Fatalf("sent %q", bodies)
	}
	// The first follow starts from now; the next carries on after the last line.
	if len(sinces) < 2 || sinces[0] == "" || sinces[1] != "1795860001.500000001" {
		t.Fatalf("since %q", sinces)
	}
}
