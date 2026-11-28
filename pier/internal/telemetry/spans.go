package telemetry

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	colt "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	resource "go.opentelemetry.io/proto/otlp/resource/v1"
	trace "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

// RouterService is the service name of the span Pier's router writes for
// every request, the root the app's own spans hang from.
const RouterService = "pier"

// Request is one request from the access log.
type Request struct {
	Project, Env, Deployment string
	Trace, Span              string // hex, when Caddy traced it
	Method, Path, Host       string
	Status                   int
	Start                    time.Time
	Duration                 time.Duration
	Bytes                    int64
	// What Analytics read from its headers.
	Browser, Device, Country, Referrer, Bot string
}

// SendRequests writes each request to Loki as a log line of Pier's router,
// and the traced ones to Tempo as spans, under their project. It gives up
// quietly when the stack is off or a server is away: they're worth less
// than not holding up the request log.
func (m *Manager) SendRequests(ctx context.Context, reqs []Request) {
	if len(reqs) == 0 || !m.On(ctx) {
		return
	}
	byProject := map[string][]Request{}
	for _, r := range reqs {
		byProject[r.Project] = append(byProject[r.Project], r)
	}
	for project, rs := range byProject {
		if err := m.sendRequestLogs(ctx, project, rs); err != nil {
			m.Log.Debug("telemetry: request logs", "project", project, "err", err)
		}
		if err := m.sendRequests(ctx, project, rs); err != nil {
			m.Log.Debug("telemetry: request spans", "project", project, "err", err)
		}
	}
}

func (m *Manager) sendRequests(ctx context.Context, project string, rs []Request) error {
	// One resource per environment and deployment.
	type key struct{ env, dep string }
	groups := map[key][]*trace.Span{}
	var order []key
	for _, r := range rs {
		if r.Trace == "" {
			continue
		}
		traceID, err1 := hex.DecodeString(r.Trace)
		spanID, err2 := hex.DecodeString(r.Span)
		if err1 != nil || err2 != nil || len(traceID) != 16 || len(spanID) != 8 {
			continue
		}
		path, query, _ := strings.Cut(r.Path, "?")
		attrs := []*common.KeyValue{
			str("http.request.method", r.Method), str("url.path", path), str("server.address", r.Host),
			num("http.response.status_code", int64(r.Status)), num("http.response.body.size", r.Bytes),
		}
		if query != "" {
			// The query's names, never its values: they can hold tokens.
			attrs = append(attrs, str("url.query", queryNames(query)))
		}
		span := &trace.Span{
			TraceId: traceID, SpanId: spanID, Name: r.Method + " " + path, Kind: trace.Span_SPAN_KIND_SERVER,
			StartTimeUnixNano: uint64(r.Start.UnixNano()), EndTimeUnixNano: uint64(r.Start.Add(r.Duration).UnixNano()),
			Attributes: attrs,
		}
		if r.Status >= 500 {
			span.Status = &trace.Status{Code: trace.Status_STATUS_CODE_ERROR, Message: fmt.Sprintf("%d %s", r.Status, http.StatusText(r.Status))}
		}
		k := key{r.Env, r.Deployment}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], span)
	}
	if len(order) == 0 {
		return nil
	}
	req := &colt.ExportTraceServiceRequest{}
	defer func() {
		for _, rs := range req.ResourceSpans {
			m.graph().observe(project, attr(rs.Resource.Attributes, "deployment.environment"), []*trace.ResourceSpans{rs})
		}
	}()
	for _, k := range order {
		res := &resource.Resource{Attributes: []*common.KeyValue{str("service.name", RouterService), str("pier.project", project), str("pier.deployment", k.dep)}}
		if k.env != "" {
			res.Attributes = append(res.Attributes, str("deployment.environment", k.env))
		}
		req.ResourceSpans = append(req.ResourceSpans, &trace.ResourceSpans{
			Resource:   res,
			ScopeSpans: []*trace.ScopeSpans{{Scope: &common.InstrumentationScope{Name: "pier.router"}, Spans: groups[k]}},
		})
	}
	body, err := proto.Marshal(req)
	if err != nil {
		return err
	}
	addr, err := m.address(ctx, Tempo, tempoOTLP)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	post, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/v1/traces", bytes.NewReader(body))
	if err != nil {
		return err
	}
	post.Header.Set("Content-Type", "application/x-protobuf")
	post.Header.Set("X-Scope-OrgID", project)
	resp, err := m.client().Do(post)
	if err != nil {
		m.forget()
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("tempo said %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

func str(k, v string) *common.KeyValue {
	return &common.KeyValue{Key: k, Value: &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: v}}}
}

func num(k string, v int64) *common.KeyValue {
	return &common.KeyValue{Key: k, Value: &common.AnyValue{Value: &common.AnyValue_IntValue{IntValue: v}}}
}

// queryNames keeps a query string's names: "q=shoes&page=2" is "q&page".
func queryNames(q string) string {
	var names []string
	for _, part := range strings.Split(q, "&") {
		name, _, _ := strings.Cut(part, "=")
		if name != "" {
			names = append(names, name)
		}
	}
	return strings.Join(names, "&")
}
