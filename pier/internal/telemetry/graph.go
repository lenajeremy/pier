package telemetry

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	colm "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	metrics "go.opentelemetry.io/proto/otlp/metrics/v1"
	resource "go.opentelemetry.io/proto/otlp/resource/v1"
	trace "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

// The service map comes from the spans pierd forwards: a span whose parent
// belongs to another service is a call from that service to this one; a
// client span to a database is a call to the database; a client span no
// other service answered is a call outside Pier. pierd counts calls,
// errors and time for each pair and each service, and writes the counts
// to Prometheus as metrics of the project:
//
//	pier_graph_calls_total{client, server, connection}
//	pier_graph_errors_total{client, server, connection}
//	pier_graph_duration_seconds_bucket{client, server, connection, le}
//	pier_service_calls_total{service}, pier_service_errors_total, pier_service_duration_seconds_bucket
//
// connection is "" between Pier's services, "database" or "external".

// How long a span waits for the other half of its pair.
const pairWithin = 15 * time.Second

var graphBounds = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

type tally struct {
	calls, errors uint64
	sum           float64
	buckets       []uint64 // one more than graphBounds: the last is +Inf
}

func (t *tally) add(seconds float64, failed bool) {
	if t.buckets == nil {
		t.buckets = make([]uint64, len(graphBounds)+1)
	}
	t.calls++
	if failed {
		t.errors++
	}
	t.sum += seconds
	i := sort.SearchFloat64s(graphBounds, seconds)
	t.buckets[i]++
}

type edgeKey struct{ project, env, client, server, connection string }
type nodeKey struct{ project, env, service string }

type seenSpan struct {
	project, env, service string
	at                    time.Time
	peer                  string // a client span's server, for a call outside Pier
	seconds               float64
	failed, paired        bool
}

type waiting struct {
	env, service string
	at           time.Time
	seconds      float64
	failed       bool
}

type graph struct {
	mu      sync.Mutex
	start   time.Time
	spans   map[string]*seenSpan // project/span ID
	waiting map[string][]waiting // project/parent span ID: children whose parent hasn't come yet
	edges   map[edgeKey]*tally
	nodes   map[nodeKey]*tally
}

func newGraph() *graph {
	return &graph{start: time.Now(), spans: map[string]*seenSpan{}, waiting: map[string][]waiting{}, edges: map[edgeKey]*tally{}, nodes: map[nodeKey]*tally{}}
}

func (m *Manager) graph() *graph {
	m.graphOnce.Do(func() { m.graphs = newGraph() })
	return m.graphs
}

func attr(attrs []*common.KeyValue, keys ...string) string {
	for _, k := range keys {
		for _, kv := range attrs {
			if kv.Key == k {
				if v := value(kv.Value); v != "" {
					return v
				}
			}
		}
	}
	return ""
}

// observe takes a batch of a project's spans.
func (g *graph) observe(project, env string, batch []*trace.ResourceSpans) {
	now := time.Now()
	g.mu.Lock()
	defer g.mu.Unlock()
	const most = 200_000
	for _, rs := range batch {
		service := attr(rs.GetResource().GetAttributes(), "service.name")
		if service == "" {
			continue
		}
		for _, ss := range rs.ScopeSpans {
			for _, sp := range ss.Spans {
				seconds := float64(int64(sp.EndTimeUnixNano-sp.StartTimeUnixNano)) / 1e9
				if seconds < 0 {
					seconds = 0
				}
				failed := sp.GetStatus().GetCode() == trace.Status_STATUS_CODE_ERROR
				root := len(sp.ParentSpanId) == 0
				if sp.Kind == trace.Span_SPAN_KIND_SERVER || sp.Kind == trace.Span_SPAN_KIND_CONSUMER || root {
					g.node(nodeKey{project, env, service}).add(seconds, failed)
				}
				// A call from the parent's service, if the parent is here.
				if !root {
					pk := project + "/" + hex.EncodeToString(sp.ParentSpanId)
					if p, ok := g.spans[pk]; ok {
						if p.service != service {
							g.edge(edgeKey{project, env, p.service, service, ""}).add(seconds, failed)
							p.paired = true
						}
					} else if len(g.waiting) < most {
						g.waiting[pk] = append(g.waiting[pk], waiting{env, service, now, seconds, failed})
					}
				}
				key := project + "/" + hex.EncodeToString(sp.SpanId)
				s := &seenSpan{project: project, env: env, service: service, at: now, seconds: seconds, failed: failed}
				// Children that came first.
				for _, c := range g.waiting[key] {
					if c.service != service {
						g.edge(edgeKey{project, c.env, service, c.service, ""}).add(c.seconds, c.failed)
						s.paired = true
					}
				}
				delete(g.waiting, key)
				if sp.Kind == trace.Span_SPAN_KIND_CLIENT || sp.Kind == trace.Span_SPAN_KIND_PRODUCER {
					if db := attr(sp.Attributes, "db.system.name", "db.system"); db != "" {
						g.edge(edgeKey{project, env, service, db, "database"}).add(seconds, failed)
						s.paired = true
					} else if peer := peerOf(sp.Attributes); peer != "" {
						s.peer = peer
					}
				}
				if len(g.spans) < most {
					g.spans[key] = s
				}
			}
		}
	}
}

// peerOf names the server a client span called.
func peerOf(attrs []*common.KeyValue) string {
	if v := attr(attrs, "messaging.system"); v != "" {
		return v
	}
	if v := attr(attrs, "server.address", "net.peer.name", "http.host"); v != "" {
		return v
	}
	if v := attr(attrs, "url.full", "http.url"); v != "" {
		if u, err := url.Parse(v); err == nil && u.Hostname() != "" {
			return u.Hostname()
		}
	}
	return ""
}

func (g *graph) node(k nodeKey) *tally {
	t := g.nodes[k]
	if t == nil {
		t = &tally{}
		g.nodes[k] = t
	}
	return t
}

func (g *graph) edge(k edgeKey) *tally {
	t := g.edges[k]
	if t == nil {
		t = &tally{}
		g.edges[k] = t
	}
	return t
}

// sweep settles spans that waited long enough: a client span no service
// answered was a call outside Pier.
func (g *graph) sweep(now time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for k, s := range g.spans {
		if now.Sub(s.at) < pairWithin {
			continue
		}
		if s.peer != "" && !s.paired {
			g.edge(edgeKey{s.project, s.env, s.service, s.peer, "external"}).add(s.seconds, s.failed)
		}
		delete(g.spans, k)
	}
	for k, cs := range g.waiting {
		if len(cs) == 0 || now.Sub(cs[0].at) >= pairWithin {
			delete(g.waiting, k)
		}
	}
}

// export writes the counts so far, per project, as OTLP metrics.
func (g *graph) export(now time.Time) map[string]*colm.ExportMetricsServiceRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	start, at := uint64(g.start.UnixNano()), uint64(now.UnixNano())
	out := map[string]*colm.ExportMetricsServiceRequest{}
	type pe struct{ project, env string }
	byEnv := map[pe]*metricSet{}
	set := func(project, env string) *metricSet {
		k := pe{project, env}
		if byEnv[k] == nil {
			byEnv[k] = &metricSet{}
		}
		return byEnv[k]
	}
	for k, t := range g.edges {
		labels := []*common.KeyValue{str("client", k.client), str("server", k.server), str("connection", k.connection)}
		s := set(k.project, k.env)
		s.add("pier_graph", labels, t, start, at)
	}
	for k, t := range g.nodes {
		s := set(k.project, k.env)
		s.add("pier_service", []*common.KeyValue{str("service", k.service)}, t, start, at)
	}
	for k, s := range byEnv {
		req := out[k.project]
		if req == nil {
			req = &colm.ExportMetricsServiceRequest{}
			out[k.project] = req
		}
		res := &resource.Resource{Attributes: []*common.KeyValue{str("service.name", "pier-graph"), str("pier.project", k.project)}}
		if k.env != "" {
			res.Attributes = append(res.Attributes, str("deployment.environment", k.env))
		}
		req.ResourceMetrics = append(req.ResourceMetrics, &metrics.ResourceMetrics{Resource: res, ScopeMetrics: []*metrics.ScopeMetrics{{
			Scope: &common.InstrumentationScope{Name: "pier.graph"}, Metrics: s.list(),
		}}})
	}
	return out
}

// metricSet gathers each metric's data points.
type metricSet struct {
	order []string
	sums  map[string][]*metrics.NumberDataPoint
	hists map[string][]*metrics.HistogramDataPoint
}

func (s *metricSet) add(prefix string, labels []*common.KeyValue, t *tally, start, at uint64) {
	if s.sums == nil {
		s.sums, s.hists = map[string][]*metrics.NumberDataPoint{}, map[string][]*metrics.HistogramDataPoint{}
	}
	if _, ok := s.hists[prefix+"_duration"]; !ok {
		s.order = append(s.order, prefix)
	}
	s.sums[prefix+"_calls"] = append(s.sums[prefix+"_calls"], &metrics.NumberDataPoint{Attributes: labels, StartTimeUnixNano: start, TimeUnixNano: at, Value: &metrics.NumberDataPoint_AsInt{AsInt: int64(t.calls)}})
	s.sums[prefix+"_errors"] = append(s.sums[prefix+"_errors"], &metrics.NumberDataPoint{Attributes: labels, StartTimeUnixNano: start, TimeUnixNano: at, Value: &metrics.NumberDataPoint_AsInt{AsInt: int64(t.errors)}})
	sum := t.sum
	s.hists[prefix+"_duration"] = append(s.hists[prefix+"_duration"], &metrics.HistogramDataPoint{
		Attributes: labels, StartTimeUnixNano: start, TimeUnixNano: at, Count: t.calls, Sum: &sum, BucketCounts: append([]uint64(nil), t.buckets...), ExplicitBounds: graphBounds,
	})
}

func (s *metricSet) list() []*metrics.Metric {
	var out []*metrics.Metric
	for _, p := range s.order {
		for _, name := range []string{p + "_calls", p + "_errors"} {
			out = append(out, &metrics.Metric{Name: name, Data: &metrics.Metric_Sum{Sum: &metrics.Sum{
				IsMonotonic: true, AggregationTemporality: metrics.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE, DataPoints: s.sums[name],
			}}})
		}
		out = append(out, &metrics.Metric{Name: p + "_duration", Unit: "s", Data: &metrics.Metric_Histogram{Histogram: &metrics.Histogram{
			AggregationTemporality: metrics.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE, DataPoints: s.hists[p+"_duration"],
		}}})
	}
	return out
}

// RunGraph settles and writes the service map's counts every 15 seconds,
// while the stack is on, until ctx ends.
func (m *Manager) RunGraph(ctx context.Context) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			g := m.graph()
			g.sweep(now)
			if !m.On(ctx) {
				continue
			}
			for project, req := range g.export(now) {
				if err := m.postMetrics(ctx, project, req); err != nil {
					m.Log.Debug("telemetry: service map", "project", project, "err", err)
				}
			}
		}
	}
}

func (m *Manager) postMetrics(ctx context.Context, project string, req *colm.ExportMetricsServiceRequest) error {
	body, err := proto.Marshal(req)
	if err != nil {
		return err
	}
	addr, err := m.address(ctx, Prometheus, promAPI)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	post, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/api/v1/otlp/v1/metrics", bytes.NewReader(body))
	if err != nil {
		return err
	}
	post.Header.Set("Content-Type", "application/x-protobuf")
	resp, err := m.client().Do(post)
	if err != nil {
		m.forget()
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("prometheus said %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}
