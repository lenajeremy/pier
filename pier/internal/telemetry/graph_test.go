package telemetry

import (
	"testing"
	"time"

	common "go.opentelemetry.io/proto/otlp/common/v1"
	resource "go.opentelemetry.io/proto/otlp/resource/v1"
	trace "go.opentelemetry.io/proto/otlp/trace/v1"
)

func batch(service string, spans ...*trace.Span) []*trace.ResourceSpans {
	return []*trace.ResourceSpans{{Resource: &resource.Resource{Attributes: []*common.KeyValue{str("service.name", service)}}, ScopeSpans: []*trace.ScopeSpans{{Spans: spans}}}}
}

func span(id, parent byte, kind trace.Span_SpanKind, ms int, failed bool, attrs ...*common.KeyValue) *trace.Span {
	s := &trace.Span{SpanId: []byte{id, 0, 0, 0, 0, 0, 0, 1}, Kind: kind, StartTimeUnixNano: 1e9, EndTimeUnixNano: 1e9 + uint64(ms)*1e6, Attributes: attrs}
	if parent != 0 {
		s.ParentSpanId = []byte{parent, 0, 0, 0, 0, 0, 0, 1}
	}
	if failed {
		s.Status = &trace.Status{Code: trace.Status_STATUS_CODE_ERROR}
	}
	return s
}

func TestServiceMap(t *testing.T) {
	g := newGraph()
	server, client := trace.Span_SPAN_KIND_SERVER, trace.Span_SPAN_KIND_CLIENT
	// The app's spans come before the router's.
	g.observe("shop", "production", batch("api",
		span(2, 1, server, 80, false),
		span(3, 2, client, 30, false, str("db.system", "postgresql")),
		span(4, 2, client, 40, true, str("server.address", "api.stripe.com")),
		span(5, 2, client, 20, false, str("server.address", "billing")),
	))
	g.observe("shop", "production", batch("billing", span(6, 5, server, 15, false)))
	g.observe("shop", "production", batch("pier", span(1, 0, server, 90, false)))
	g.sweep(time.Now().Add(pairWithin + time.Second))
	edge := func(c, s, conn string) tally {
		if t := g.edges[edgeKey{"shop", "production", c, s, conn}]; t != nil {
			return *t
		}
		return tally{}
	}
	if e := edge("pier", "api", ""); e.calls != 1 || e.sum != 0.08 {
		t.Fatalf("pier to api: %+v", e)
	}
	if e := edge("api", "postgresql", "database"); e.calls != 1 {
		t.Fatalf("api to postgres: %+v", e)
	}
	if e := edge("api", "api.stripe.com", "external"); e.calls != 1 || e.errors != 1 {
		t.Fatalf("api to stripe: %+v", e)
	}
	if e := edge("api", "billing", ""); e.calls != 1 {
		t.Fatalf("api to billing: %+v", e)
	}
	if e := edge("api", "billing", "external"); e.calls != 0 {
		t.Fatalf("a call billing answered counted as outside Pier: %+v", e)
	}
	for _, s := range []string{"pier", "api", "billing"} {
		if n := g.nodes[nodeKey{"shop", "production", s}]; n == nil || n.calls != 1 {
			t.Fatalf("node %s: %+v", s, n)
		}
	}
	if len(g.spans) != 0 || len(g.waiting) != 0 {
		t.Fatalf("left behind: %d spans, %d waiting", len(g.spans), len(g.waiting))
	}
	req := g.export(time.Now())["shop"]
	if req == nil || len(req.ResourceMetrics) != 1 || len(req.ResourceMetrics[0].ScopeMetrics[0].Metrics) != 6 {
		t.Fatalf("export: %+v", req)
	}
}
