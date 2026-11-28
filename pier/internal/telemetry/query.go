package telemetry

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	colt "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	trace "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

// ErrNoTrace means Tempo has no trace by that ID for the project.
var ErrNoTrace = errors.New("there's no trace by that ID; traces are kept 14 days")

// QueryError is a query the server refused, like TraceQL that doesn't parse.
type QueryError struct{ Msg string }

func (e QueryError) Error() string { return e.Msg }

// get asks one of the stack's servers, as the project.
func (m *Manager) get(ctx context.Context, server string, port int, path string, q url.Values, accept string) ([]byte, error) {
	if !m.On(ctx) {
		return nil, ErrOff
	}
	addr, err := m.address(ctx, server, port)
	if err != nil {
		return nil, err
	}
	u := "http://" + addr + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if project, _ := ctx.Value(tenantKey{}).(string); project != "" {
		req.Header.Set("X-Scope-OrgID", project)
	}
	return m.do(req, server)
}

func (m *Manager) do(req *http.Request, server string) ([]byte, error) {
	resp, err := m.client().Do(req)
	if err != nil {
		m.forget()
		return nil, fmt.Errorf("%s didn't answer: %w", server, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrNoTrace
	case resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnprocessableEntity:
		return nil, QueryError{strings.TrimSpace(string(body))}
	case resp.StatusCode >= 300:
		return nil, fmt.Errorf("%s said %d: %s", server, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

// TraceSummary is one trace in a search.
type TraceSummary struct {
	ID       string           `json:"id"`
	Service  string           `json:"service"` // the root span's
	Name     string           `json:"name"`
	Start    float64          `json:"start"` // ms since the epoch
	MS       float64          `json:"ms"`
	Spans    int              `json:"spans"`
	Errors   int              `json:"errors"`
	Services map[string]Count `json:"services"`
	// Partial means the root span hasn't arrived yet: Pier's router sends
	// its span a second or so after the app's.
	Partial bool `json:"partial,omitempty"`
}

// Count is one service's spans in a trace.
type Count struct {
	Spans  int `json:"spans"`
	Errors int `json:"errors,omitempty"`
}

// Search finds a project's traces that match a TraceQL query, newest first.
func (m *Manager) Search(ctx context.Context, project, query string, from, to time.Time, limit int) ([]TraceSummary, error) {
	if strings.TrimSpace(query) == "" {
		query = "{}"
	}
	q := url.Values{"q": {query}, "start": {strconv.FormatInt(from.Unix(), 10)}, "end": {strconv.FormatInt(to.Unix()+1, 10)}, "limit": {strconv.Itoa(limit)}, "spss": {"1"}}
	ctx = tenant(ctx, project)
	body, err := m.get(ctx, Tempo, tempoAPI, "/api/search", q, "")
	if err != nil {
		return nil, err
	}
	var res struct {
		Traces []struct {
			TraceID           string  `json:"traceID"`
			RootServiceName   string  `json:"rootServiceName"`
			RootTraceName     string  `json:"rootTraceName"`
			StartTimeUnixNano string  `json:"startTimeUnixNano"`
			DurationMs        float64 `json:"durationMs"`
			ServiceStats      map[string]struct {
				SpanCount  int `json:"spanCount"`
				ErrorCount int `json:"errorCount"`
			} `json:"serviceStats"`
		} `json:"traces"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, err
	}
	out := make([]TraceSummary, 0, len(res.Traces))
	for _, t := range res.Traces {
		ns, _ := strconv.ParseInt(t.StartTimeUnixNano, 10, 64)
		s := TraceSummary{ID: padID(t.TraceID), Service: t.RootServiceName, Name: t.RootTraceName, Start: float64(ns) / 1e6, MS: t.DurationMs, Services: map[string]Count{}}
		if strings.HasPrefix(s.Service, "<") {
			s.Service, s.Name, s.Partial = "", "", true
		}
		for name, st := range t.ServiceStats {
			s.Services[name] = Count{Spans: st.SpanCount, Errors: st.ErrorCount}
			s.Spans += st.SpanCount
			s.Errors += st.ErrorCount
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start > out[j].Start })
	return out, nil
}

// padID puts back the leading zeros Tempo leaves off a trace ID.
func padID(id string) string {
	if len(id) < 32 {
		return strings.Repeat("0", 32-len(id)) + id
	}
	return id
}

// Span is one span of a trace, for the waterfall.
type Span struct {
	ID       string      `json:"id"`
	Parent   string      `json:"parent,omitempty"`
	Service  string      `json:"service"`
	Name     string      `json:"name"`
	Kind     string      `json:"kind,omitempty"` // server, client, producer, consumer, internal
	Start    float64     `json:"start"`          // ms after the trace's start
	MS       float64     `json:"ms"`
	Error    bool        `json:"error,omitempty"`
	Message  string      `json:"message,omitempty"`
	Attrs    [][2]string `json:"attrs,omitempty"`
	Resource [][2]string `json:"resource,omitempty"`
	Events   []SpanEvent `json:"events,omitempty"`
}

// SpanEvent is something a span recorded at a moment, like an exception.
type SpanEvent struct {
	Name  string      `json:"name"`
	At    float64     `json:"at"` // ms after the trace's start
	Attrs [][2]string `json:"attrs,omitempty"`
}

// Trace is a whole trace.
type Trace struct {
	ID    string  `json:"id"`
	Start float64 `json:"start"` // ms since the epoch
	MS    float64 `json:"ms"`
	Spans []Span  `json:"spans"`
}

// Trace reads one of a project's traces.
func (m *Manager) Trace(ctx context.Context, project, id string) (Trace, error) {
	id = strings.ToLower(id)
	if !isTraceID(id) {
		return Trace{}, ErrNoTrace
	}
	body, err := m.get(tenant(ctx, project), Tempo, tempoAPI, "/api/traces/"+id, nil, "application/protobuf")
	if err != nil {
		return Trace{}, err
	}
	// Tempo's Trace message has the export request's shape: its batches
	// are field 1, as resource_spans are.
	var raw colt.ExportTraceServiceRequest
	if err := proto.Unmarshal(body, &raw); err != nil {
		return Trace{}, fmt.Errorf("read the trace: %w", err)
	}
	t := Trace{ID: id}
	var first, last uint64
	for _, rs := range raw.ResourceSpans {
		for _, ss := range rs.ScopeSpans {
			for _, sp := range ss.Spans {
				if first == 0 || sp.StartTimeUnixNano < first {
					first = sp.StartTimeUnixNano
				}
				if sp.EndTimeUnixNano > last {
					last = sp.EndTimeUnixNano
				}
			}
		}
	}
	if first == 0 {
		return Trace{}, ErrNoTrace
	}
	at := func(ns uint64) float64 { return float64(int64(ns-first)) / 1e6 }
	for _, rs := range raw.ResourceSpans {
		res := pairs(rs.GetResource().GetAttributes())
		service := ""
		for _, kv := range res {
			if kv[0] == "service.name" {
				service = kv[1]
			}
		}
		for _, ss := range rs.ScopeSpans {
			for _, sp := range ss.Spans {
				s := Span{
					ID: hex.EncodeToString(sp.SpanId), Service: service, Name: sp.Name, Kind: kind(sp.Kind),
					Start: at(sp.StartTimeUnixNano), MS: float64(int64(sp.EndTimeUnixNano-sp.StartTimeUnixNano)) / 1e6,
					Error: sp.GetStatus().GetCode() == trace.Status_STATUS_CODE_ERROR, Message: sp.GetStatus().GetMessage(),
					Attrs: pairs(sp.Attributes), Resource: res,
				}
				if len(sp.ParentSpanId) > 0 {
					s.Parent = hex.EncodeToString(sp.ParentSpanId)
				}
				for _, e := range sp.Events {
					s.Events = append(s.Events, SpanEvent{Name: e.Name, At: at(e.TimeUnixNano), Attrs: pairs(e.Attributes)})
				}
				t.Spans = append(t.Spans, s)
			}
		}
	}
	sort.SliceStable(t.Spans, func(i, j int) bool { return t.Spans[i].Start < t.Spans[j].Start })
	t.Start = float64(first) / 1e6
	t.MS = float64(last-first) / 1e6
	return t, nil
}

func isTraceID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func kind(k trace.Span_SpanKind) string {
	switch k {
	case trace.Span_SPAN_KIND_SERVER:
		return "server"
	case trace.Span_SPAN_KIND_CLIENT:
		return "client"
	case trace.Span_SPAN_KIND_PRODUCER:
		return "producer"
	case trace.Span_SPAN_KIND_CONSUMER:
		return "consumer"
	case trace.Span_SPAN_KIND_INTERNAL:
		return "internal"
	}
	return ""
}

func pairs(kvs []*common.KeyValue) [][2]string {
	out := make([][2]string, 0, len(kvs))
	for _, kv := range kvs {
		out = append(out, [2]string{kv.Key, value(kv.Value)})
	}
	return out
}

func value(v *common.AnyValue) string {
	switch x := v.GetValue().(type) {
	case *common.AnyValue_StringValue:
		return x.StringValue
	case *common.AnyValue_IntValue:
		return strconv.FormatInt(x.IntValue, 10)
	case *common.AnyValue_DoubleValue:
		return strconv.FormatFloat(x.DoubleValue, 'g', -1, 64)
	case *common.AnyValue_BoolValue:
		return strconv.FormatBool(x.BoolValue)
	case *common.AnyValue_ArrayValue:
		var parts []string
		for _, e := range x.ArrayValue.GetValues() {
			parts = append(parts, value(e))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *common.AnyValue_KvlistValue:
		var parts []string
		for _, kv := range x.KvlistValue.GetValues() {
			parts = append(parts, kv.Key+"="+value(kv.Value))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case *common.AnyValue_BytesValue:
		return hex.EncodeToString(x.BytesValue)
	}
	return ""
}

// LogLine is one line an app logged.
type LogLine struct {
	At      float64 `json:"at"` // ms since the epoch
	Level   string  `json:"level,omitempty"`
	Service string  `json:"service,omitempty"`
	Span    string  `json:"span,omitempty"`
	Stream  string  `json:"stream,omitempty"`
	Text    string  `json:"text"`
}

// TraceLogs reads the lines logged with a trace's ID, oldest first.
func (m *Manager) TraceLogs(ctx context.Context, project, id string, from, to time.Time) ([]LogLine, error) {
	if !isTraceID(id) {
		return nil, ErrNoTrace
	}
	return m.Logs(ctx, project, `{service_name=~".+"} | trace_id="`+id+`"`, from, to, 500, true)
}

// Logs runs a LogQL query over a project's logs.
func (m *Manager) Logs(ctx context.Context, project, query string, from, to time.Time, limit int, oldestFirst bool) ([]LogLine, error) {
	direction := "backward"
	if oldestFirst {
		direction = "forward"
	}
	q := url.Values{"query": {query}, "start": {strconv.FormatInt(from.UnixNano(), 10)}, "end": {strconv.FormatInt(to.UnixNano(), 10)},
		"limit": {strconv.Itoa(limit)}, "direction": {direction}}
	body, err := m.get(tenant(ctx, project), Loki, lokiAPI, "/loki/api/v1/query_range", q, "")
	if err != nil {
		if errors.Is(err, ErrNoTrace) {
			return nil, nil
		}
		return nil, err
	}
	var res struct {
		Data struct {
			Result []struct {
				Stream map[string]string `json:"stream"`
				Values [][]string        `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, err
	}
	var out []LogLine
	for _, r := range res.Data.Result {
		for _, v := range r.Values {
			if len(v) < 2 {
				continue
			}
			ns, _ := strconv.ParseInt(v[0], 10, 64)
			out = append(out, LogLine{At: float64(ns) / 1e6, Level: r.Stream["detected_level"], Service: r.Stream["service_name"],
				Span: r.Stream["span_id"], Stream: r.Stream["log_iostream"], Text: v[1]})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if oldestFirst {
			return out[i].At < out[j].At
		}
		return out[i].At > out[j].At
	})
	return out, nil
}

type tenantKey struct{}

// tenant marks a request as the project's, for Tempo and Loki.
func tenant(ctx context.Context, project string) context.Context {
	return context.WithValue(ctx, tenantKey{}, project)
}
