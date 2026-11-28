package telemetry

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	colt "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	resource "go.opentelemetry.io/proto/otlp/resource/v1"
	trace "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"pier/internal/apphost"
	"pier/internal/store"
)

type fakeContainers struct{ addr string }

func (f *fakeContainers) StartService(context.Context, apphost.ServiceSpec) error { return nil }
func (f *fakeContainers) StopService(context.Context, string) error               { return nil }
func (f *fakeContainers) ServiceAddress(context.Context, string, int) (string, error) {
	return f.addr, nil
}
func (f *fakeContainers) CopyFile(context.Context, string, string, []byte) error { return nil }
func (f *fakeContainers) ServiceStatus(context.Context, string) (apphost.ServiceState, error) {
	return apphost.ServiceState{State: "running"}, nil
}

func TestForwardStampsTheProject(t *testing.T) {
	ctx := context.Background()
	var got *colt.ExportTraceServiceRequest
	var tenant, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = &colt.ExportTraceServiceRequest{}
		if err := proto.Unmarshal(b, got); err != nil {
			t.Errorf("forwarded something that isn't protobuf: %v", err)
		}
		tenant, path = r.Header.Get("X-Scope-OrgID"), r.URL.Path
	}))
	defer srv.Close()
	st, err := store.Open(filepath.Join(t.TempDir(), "pier.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := &Manager{Store: st, Containers: &fakeContainers{addr: strings.TrimPrefix(srv.URL, "http://")}, Dir: t.TempDir(), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	req := &colt.ExportTraceServiceRequest{ResourceSpans: []*trace.ResourceSpans{
		// An app claiming to be another project.
		{Resource: &resource.Resource{Attributes: []*common.KeyValue{str("pier.project", "bank"), str("service.name", "api")}},
			ScopeSpans: []*trace.ScopeSpans{{Spans: []*trace.Span{{Name: "GET /api/orders", TraceId: make([]byte, 16), SpanId: make([]byte, 8)}}}}},
		{ScopeSpans: []*trace.ScopeSpans{{Spans: []*trace.Span{{Name: "worker", TraceId: make([]byte, 16), SpanId: make([]byte, 8)}}}}},
	}}
	body, _ := proto.Marshal(req)
	if code, err := m.Forward(ctx, Traces, "shop", "production", "application/x-protobuf", "", body); code != http.StatusServiceUnavailable || err == nil {
		t.Fatalf("forwarded while off: %d %v", code, err)
	}
	if err := m.Enable(ctx); err != nil {
		t.Fatal(err)
	}
	if code, err := m.Forward(ctx, Traces, "shop", "production", "application/x-protobuf", "", body); code != 200 || err != nil {
		t.Fatalf("forward: %d %v", code, err)
	}
	if tenant != "shop" || path != "/v1/traces" {
		t.Fatalf("tenant %q path %q", tenant, path)
	}
	attrs := func(i int) map[string]string {
		out := map[string]string{}
		for _, kv := range got.ResourceSpans[i].Resource.Attributes {
			out[kv.Key] = kv.Value.GetStringValue()
		}
		return out
	}
	if a := attrs(0); a["pier.project"] != "shop" || a["deployment.environment"] != "production" || a["service.name"] != "api" {
		t.Fatalf("first resource %v", a)
	}
	if a := attrs(1); a["pier.project"] != "shop" || a["service.name"] != "shop" {
		t.Fatalf("a resource with no service is named after the project: %v", a)
	}
	// JSON, gzipped.
	j, _ := protojson.Marshal(req)
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	w.Write(j)
	w.Close()
	if code, err := m.Forward(ctx, Traces, "shop", "preview", "application/json", "gzip", gz.Bytes()); code != 200 || err != nil || attrs(0)["deployment.environment"] != "preview" {
		t.Fatalf("json: %d %v %v", code, err, attrs(0))
	}
	if code, _ := m.Forward(ctx, Traces, "shop", "production", "application/x-protobuf", "", []byte("not protobuf at all")); code != http.StatusBadRequest {
		t.Fatalf("garbage: %d", code)
	}
	if code, _ := m.Forward(ctx, "profiles", "shop", "production", "application/x-protobuf", "", body); code != http.StatusNotFound {
		t.Fatalf("an unknown signal: %d", code)
	}
}

func TestHexIDs(t *testing.T) {
	body := []byte(`{"resourceSpans":[{"scopeSpans":[{"spans":[{"traceId":"5b8efff798038103d269b633813fc60c","spanId":"eee19b7ec3c1b174","parentSpanId":"","name":"x","startTimeUnixNano":"1795000000000000000"}]}]}]}`)
	fixed, err := hexIDs(body)
	if err != nil {
		t.Fatal(err)
	}
	var r colt.ExportTraceServiceRequest
	if err := protojson.Unmarshal(fixed, &r); err != nil {
		t.Fatal(err)
	}
	span := r.ResourceSpans[0].ScopeSpans[0].Spans[0]
	if len(span.TraceId) != 16 || len(span.SpanId) != 8 || span.StartTimeUnixNano != 1795000000000000000 {
		t.Fatalf("%x %x %d", span.TraceId, span.SpanId, span.StartTimeUnixNano)
	}
}
