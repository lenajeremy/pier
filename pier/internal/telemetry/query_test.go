package telemetry

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

	colt "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	resource "go.opentelemetry.io/proto/otlp/resource/v1"
	trace "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"pier/internal/store"
)

func TestSearchAndReadATrace(t *testing.T) {
	id := "0b3f6cf2a9d4e1f00112233445566778"
	tid := make([]byte, 16)
	copy(tid, []byte{0x0b, 0x3f, 0x6c, 0xf2, 0xa9, 0xd4, 0xe1, 0xf0, 0x01, 0x12, 0x23, 0x34, 0x45, 0x56, 0x67, 0x78})
	start := uint64(time.Date(2026, 11, 28, 10, 0, 0, 0, time.UTC).UnixNano())
	doc := &colt.ExportTraceServiceRequest{ResourceSpans: []*trace.ResourceSpans{
		{Resource: &resource.Resource{Attributes: []*common.KeyValue{str("service.name", "pier")}}, ScopeSpans: []*trace.ScopeSpans{{Spans: []*trace.Span{
			{TraceId: tid, SpanId: []byte{1, 1, 1, 1, 1, 1, 1, 1}, Name: "GET /api/orders", Kind: trace.Span_SPAN_KIND_SERVER, StartTimeUnixNano: start, EndTimeUnixNano: start + 80e6,
				Status: &trace.Status{Code: trace.Status_STATUS_CODE_ERROR}},
		}}}},
		{Resource: &resource.Resource{Attributes: []*common.KeyValue{str("service.name", "api")}}, ScopeSpans: []*trace.ScopeSpans{{Spans: []*trace.Span{
			{TraceId: tid, SpanId: []byte{2, 2, 2, 2, 2, 2, 2, 2}, ParentSpanId: []byte{1, 1, 1, 1, 1, 1, 1, 1}, Name: "SELECT orders", StartTimeUnixNano: start + 5e6, EndTimeUnixNano: start + 65e6,
				Attributes: []*common.KeyValue{num("db.rows", 50)}, Events: []*trace.Span_Event{{Name: "exception", TimeUnixNano: start + 60e6, Attributes: []*common.KeyValue{str("exception.message", "boom")}}}},
		}}}},
	}}
	body, _ := proto.Marshal(doc)
	var tenants []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenants = append(tenants, r.Header.Get("X-Scope-OrgID"))
		switch {
		case r.URL.Path == "/api/search":
			if r.URL.Query().Get("q") != "{}" {
				t.Errorf("an empty query should search everything: %q", r.URL.Query().Get("q"))
			}
			io.WriteString(w, `{"traces":[{"traceID":"b3f6cf2a9d4e1f00112233445566778","rootServiceName":"pier","rootTraceName":"GET /api/orders","startTimeUnixNano":"1795860000000000000","durationMs":80,
				"serviceStats":{"pier":{"spanCount":1,"errorCount":1},"api":{"spanCount":1}}}]}`)
		case r.URL.Path == "/api/traces/"+id && r.Header.Get("Accept") == "application/protobuf":
			w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	st, err := store.Open(filepath.Join(t.TempDir(), "pier.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := &Manager{Store: st, Containers: &fakeContainers{addr: strings.TrimPrefix(srv.URL, "http://")}, Dir: t.TempDir(), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ctx := context.Background()
	if _, err := m.Search(ctx, "shop", "", time.Now().Add(-time.Hour), time.Now(), 20); err != ErrOff {
		t.Fatalf("searched while off: %v", err)
	}
	m.Enable(ctx)
	found, err := m.Search(ctx, "shop", " ", time.Now().Add(-time.Hour), time.Now(), 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].ID != id || found[0].Spans != 2 || found[0].Errors != 1 || found[0].MS != 80 || found[0].Start != 1795860000000 {
		t.Fatalf("%+v", found)
	}
	tr, err := m.Trace(ctx, "shop", strings.ToUpper(id))
	if err != nil {
		t.Fatal(err)
	}
	if tr.MS != 80 || len(tr.Spans) != 2 {
		t.Fatalf("%+v", tr)
	}
	root, child := tr.Spans[0], tr.Spans[1]
	if root.Service != "pier" || !root.Error || root.Kind != "server" || root.Parent != "" {
		t.Fatalf("root %+v", root)
	}
	if child.Service != "api" || child.Parent != root.ID || child.Start != 5 || child.MS != 60 || child.Attrs[0] != [2]string{"db.rows", "50"} || child.Events[0].At != 60 {
		t.Fatalf("child %+v", child)
	}
	if _, err := m.Trace(ctx, "shop", "not-an-id"); err != ErrNoTrace {
		t.Fatalf("a bad ID: %v", err)
	}
	if _, err := m.Trace(ctx, "shop", "ffffffffffffffffffffffffffffffff"); err != ErrNoTrace {
		t.Fatalf("a missing trace: %v", err)
	}
	for _, tn := range tenants {
		if tn != "shop" {
			t.Fatalf("asked as %q", tn)
		}
	}
}
