package telemetry

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"

	common "go.opentelemetry.io/proto/otlp/common/v1"
	logs "go.opentelemetry.io/proto/otlp/logs/v1"
	resource "go.opentelemetry.io/proto/otlp/resource/v1"
)

// sendRequestLogs writes requests to Loki as lines of Pier's router, so
// Logs shows them beside what the app printed, with the same fields.
func (m *Manager) sendRequestLogs(ctx context.Context, project string, rs []Request) error {
	type key struct{ env, dep string }
	groups := map[key][]*logs.LogRecord{}
	var order []key
	for _, r := range rs {
		path, query, _ := strings.Cut(r.Path, "?")
		ms := float64(r.Duration.Microseconds()) / 1000
		rec := &logs.LogRecord{
			TimeUnixNano: uint64(r.Start.Add(r.Duration).UnixNano()), ObservedTimeUnixNano: uint64(r.Start.Add(r.Duration).UnixNano()),
			Body: &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: fmt.Sprintf("%s %s %d in %s", r.Method, path, r.Status, msText(ms))}},
			Attributes: []*common.KeyValue{
				str("http.request.method", r.Method), str("url.path", path), num("http.response.status_code", int64(r.Status)),
				{Key: "duration_ms", Value: &common.AnyValue{Value: &common.AnyValue_DoubleValue{DoubleValue: ms}}},
				num("http.response.body.size", r.Bytes),
			},
		}
		switch {
		case r.Status >= 500:
			rec.SeverityText, rec.SeverityNumber = "error", logs.SeverityNumber_SEVERITY_NUMBER_ERROR
		case r.Status >= 400:
			rec.SeverityText, rec.SeverityNumber = "warn", logs.SeverityNumber_SEVERITY_NUMBER_WARN
		default:
			rec.SeverityText, rec.SeverityNumber = "info", logs.SeverityNumber_SEVERITY_NUMBER_INFO
		}
		for _, kv := range [][2]string{{"server.address", r.Host}, {"url.query", queryNames(query)}, {"browser", r.Browser}, {"device", r.Device}, {"country", r.Country}, {"referrer", r.Referrer}, {"bot", r.Bot}} {
			if kv[1] != "" {
				rec.Attributes = append(rec.Attributes, str(kv[0], kv[1]))
			}
		}
		if id, err := hex.DecodeString(r.Trace); err == nil && len(id) == 16 {
			rec.TraceId = id
			if sp, err := hex.DecodeString(r.Span); err == nil && len(sp) == 8 {
				rec.SpanId = sp
			}
		}
		k := key{r.Env, r.Deployment}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], rec)
	}
	var batch []*logs.ResourceLogs
	for _, k := range order {
		res := &resource.Resource{Attributes: []*common.KeyValue{str("service.name", RouterService), str("pier.project", project), str("pier.deployment", k.dep)}}
		if k.env != "" {
			res.Attributes = append(res.Attributes, str("deployment.environment", k.env))
		}
		batch = append(batch, &logs.ResourceLogs{Resource: res, ScopeLogs: []*logs.ScopeLogs{{Scope: &common.InstrumentationScope{Name: "pier.router"}, LogRecords: groups[k]}}})
	}
	return m.postLogs(ctx, project, batch)
}

func msText(ms float64) string {
	switch {
	case ms < 1:
		return "<1 ms"
	case ms < 1000:
		return fmt.Sprintf("%.0f ms", ms)
	}
	return fmt.Sprintf("%.2f s", ms/1000)
}
