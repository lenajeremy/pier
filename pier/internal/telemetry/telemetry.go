// Package telemetry runs the servers that keep apps' traces, logs and
// metrics, Grafana's Tempo, Loki and Prometheus, and takes what apps send
// over OpenTelemetry. An app sends to pierd with its project's token;
// pierd writes the project onto every resource, so one app can't write as
// another, and hands the data on: Tempo and Loki keep each project apart
// as a tenant, Prometheus labels every series with it.
package telemetry

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	coll "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colm "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	colt "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	resource "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"pier/internal/apphost"
	"pier/internal/store"
)

// Retention is how long each server keeps data.
const Retention = 14 * 24 * time.Hour

const settingOn = "telemetry_on"

// The stack's servers, their images and ports.
const (
	Tempo      = "tempo"
	Loki       = "loki"
	Prometheus = "prometheus"
)

var images = map[string]string{
	Tempo:      "grafana/tempo:2.8.2",
	Loki:       "grafana/loki:3.5.5",
	Prometheus: "prom/prometheus:v3.5.0",
}

// The ports pierd uses on each.
const (
	tempoAPI  = 3200
	tempoOTLP = 4318
	lokiAPI   = 3100
	promAPI   = 9090
)

// Containers runs the stack's servers.
type Containers interface {
	StartService(ctx context.Context, spec apphost.ServiceSpec) error
	StopService(ctx context.Context, name string) error
	ServiceAddress(ctx context.Context, name string, port int) (string, error)
	ServiceStatus(ctx context.Context, name string) (apphost.ServiceState, error)
}

// Manager turns the stack on and off and takes what apps send.
type Manager struct {
	Store      *store.Store
	Containers Containers
	Dir        string // where the servers' settings files are written
	Log        *slog.Logger
	Client     *http.Client
	// Apps lists the running app containers, and OpenLogs follows one's
	// output from a time (Docker's since), for FollowLogs.
	Apps     func(ctx context.Context) ([]App, error)
	OpenLogs func(ctx context.Context, container, since string) (io.ReadCloser, bool, error)

	mu    sync.Mutex
	addrs map[string]string // "tempo:4318" -> 127.0.0.1:port

	graphOnce sync.Once
	graphs    *graph // the service map's counts
}

// On reports whether the stack is turned on.
func (m *Manager) On(ctx context.Context) bool {
	v, _ := m.Store.Setting(ctx, settingOn)
	return v == "1"
}

// Enable writes the servers' settings, starts them and remembers that the
// stack is on.
func (m *Manager) Enable(ctx context.Context) error {
	if err := m.start(ctx); err != nil {
		return err
	}
	return m.Store.SetSetting(ctx, settingOn, "1")
}

// Disable stops the servers. Their data stays on their volumes.
func (m *Manager) Disable(ctx context.Context) error {
	if err := m.Store.SetSetting(ctx, settingOn, ""); err != nil {
		return err
	}
	var errs []error
	for _, name := range []string{Tempo, Loki, Prometheus} {
		if err := m.Containers.StopService(ctx, name); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}
	m.forget()
	return errors.Join(errs...)
}

// Ensure starts the stack when pierd starts, if it's on.
func (m *Manager) Ensure(ctx context.Context) {
	if !m.On(ctx) {
		return
	}
	if err := m.start(ctx); err != nil {
		m.Log.Warn("telemetry: couldn't start the servers", "err", err)
	}
}

func (m *Manager) forget() {
	m.mu.Lock()
	m.addrs = nil
	m.mu.Unlock()
}

func (m *Manager) start(ctx context.Context) error {
	if err := os.MkdirAll(m.Dir, 0o755); err != nil {
		return err
	}
	specs, err := m.specs()
	if err != nil {
		return err
	}
	var errs []error
	for _, spec := range specs {
		if err := m.Containers.StartService(ctx, spec); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", spec.Name, err))
		}
	}
	m.forget()
	return errors.Join(errs...)
}

// specs writes each server's settings file and says how to run it.
func (m *Manager) specs() ([]apphost.ServiceSpec, error) {
	hours := fmt.Sprintf("%dh", int(Retention.Hours()))
	files := map[string]string{
		Tempo: `multitenancy_enabled: true
stream_over_http_enabled: true
server:
  http_listen_port: 3200
  log_level: warn
distributor:
  receivers:
    otlp:
      protocols:
        http:
          endpoint: 0.0.0.0:4318
ingester:
  max_block_duration: 5m
compactor:
  compaction:
    block_retention: ` + hours + `
storage:
  trace:
    backend: local
    wal:
      path: /var/tempo/wal
    local:
      path: /var/tempo/blocks
`,
		Loki: `auth_enabled: true
server:
  http_listen_port: 3100
  log_level: warn
common:
  path_prefix: /loki
  replication_factor: 1
  ring:
    kvstore:
      store: inmemory
  storage:
    filesystem:
      chunks_directory: /loki/chunks
      rules_directory: /loki/rules
schema_config:
  configs:
    - from: 2024-01-01
      store: tsdb
      object_store: filesystem
      schema: v13
      index:
        prefix: index_
        period: 24h
limits_config:
  retention_period: ` + hours + `
  allow_structured_metadata: true
  volume_enabled: true
compactor:
  working_directory: /loki/compactor
  retention_enabled: true
  delete_request_store: filesystem
analytics:
  reporting_enabled: false
`,
		Prometheus: `global:
  scrape_interval: 30s
otlp:
  promote_resource_attributes: [service.name, service.instance.id, deployment.environment, pier.project, pier.deployment]
  keep_identifying_resource_attributes: true
storage:
  tsdb:
    out_of_order_time_window: 30m
`,
	}
	write := func(name string) (string, string, error) {
		path := filepath.Join(m.Dir, name+".yaml")
		if err := os.WriteFile(path, []byte(files[name]), 0o644); err != nil {
			return "", "", err
		}
		sum := sha256.Sum256([]byte(images[name] + files[name]))
		return path, hex.EncodeToString(sum[:8]), nil
	}
	tp, th, err := write(Tempo)
	if err != nil {
		return nil, err
	}
	lp, lh, err := write(Loki)
	if err != nil {
		return nil, err
	}
	pp, ph, err := write(Prometheus)
	if err != nil {
		return nil, err
	}
	return []apphost.ServiceSpec{
		{Name: Tempo, Image: images[Tempo], Cmd: []string{"-config.file=/etc/tempo.yaml"}, Ports: []int{tempoAPI, tempoOTLP},
			Files: map[string]string{tp: "/etc/tempo.yaml"}, Data: "/var/tempo", MemoryMB: 512, Config: th},
		{Name: Loki, Image: images[Loki], Cmd: []string{"-config.file=/etc/loki/pier.yaml"}, Ports: []int{lokiAPI},
			Files: map[string]string{lp: "/etc/loki/pier.yaml"}, Data: "/loki", MemoryMB: 384, Config: lh},
		{Name: Prometheus, Image: images[Prometheus], Ports: []int{promAPI}, Files: map[string]string{pp: "/etc/prometheus/prometheus.yml"}, Data: "/prometheus", MemoryMB: 384, Config: ph,
			Cmd: []string{"--config.file=/etc/prometheus/prometheus.yml", "--storage.tsdb.path=/prometheus", "--storage.tsdb.retention.time=" + hours, "--web.enable-otlp-receiver"}},
	}, nil
}

// address finds where pierd reaches one of a server's ports, and remembers
// it until the stack restarts.
func (m *Manager) address(ctx context.Context, name string, port int) (string, error) {
	key := fmt.Sprintf("%s:%d", name, port)
	m.mu.Lock()
	a, ok := m.addrs[key]
	m.mu.Unlock()
	if ok {
		return a, nil
	}
	a, err := m.Containers.ServiceAddress(ctx, name, port)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	if m.addrs == nil {
		m.addrs = map[string]string{}
	}
	m.addrs[key] = a
	m.mu.Unlock()
	return a, nil
}

func (m *Manager) client() *http.Client {
	if m.Client != nil {
		return m.Client
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// Server is one server's state, for the Server page.
type Server struct {
	Name  string               `json:"name"`
	Image string               `json:"image"`
	State apphost.ServiceState `json:"state"`
}

// Status is the stack's state.
type Status struct {
	On        bool     `json:"on"`
	Servers   []Server `json:"servers"`
	Retention int      `json:"retention_days"`
}

func (m *Manager) Status(ctx context.Context) Status {
	s := Status{On: m.On(ctx), Retention: int(Retention.Hours() / 24)}
	for _, name := range []string{Tempo, Loki, Prometheus} {
		st, err := m.Containers.ServiceStatus(ctx, name)
		if err != nil {
			st = apphost.ServiceState{State: "unknown"}
		}
		s.Servers = append(s.Servers, Server{Name: name, Image: images[name], State: st})
	}
	return s
}

// Signals are what OpenTelemetry sends.
const (
	Traces  = "traces"
	Metrics = "metrics"
	Logs    = "logs"
)

// ErrOff means the stack isn't on.
var ErrOff = errors.New("traces, metrics and logs are off on this server; the owner turns them on in Server")

// Forward takes one OpenTelemetry export from a project's app, writes the
// project and environment onto every resource in it, and hands it to the
// server that keeps that signal. It answers with the status that server
// gave and the body to send back.
func (m *Manager) Forward(ctx context.Context, signal, project, env, contentType, encoding string, body []byte) (int, error) {
	if !m.On(ctx) {
		return http.StatusServiceUnavailable, ErrOff
	}
	if encoding == "gzip" {
		zr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return http.StatusBadRequest, err
		}
		if body, err = io.ReadAll(io.LimitReader(zr, 32<<20)); err != nil {
			return http.StatusBadRequest, err
		}
	}
	isJSON := strings.HasPrefix(contentType, "application/json")
	var msg proto.Message
	var resources func(func(*resource.Resource) *resource.Resource)
	var after func() // once the resources say whose they are
	var target, path string
	var port int
	switch signal {
	case Traces:
		r := &colt.ExportTraceServiceRequest{}
		msg, target, port, path = r, Tempo, tempoOTLP, "/v1/traces"
		resources = func(f func(*resource.Resource) *resource.Resource) {
			for _, x := range r.ResourceSpans {
				x.Resource = f(x.Resource)
			}
		}
		after = func() { m.graph().observe(project, env, r.ResourceSpans) }
	case Logs:
		r := &coll.ExportLogsServiceRequest{}
		msg, target, port, path = r, Loki, lokiAPI, "/otlp/v1/logs"
		resources = func(f func(*resource.Resource) *resource.Resource) {
			for _, x := range r.ResourceLogs {
				x.Resource = f(x.Resource)
			}
		}
	case Metrics:
		r := &colm.ExportMetricsServiceRequest{}
		msg, target, port, path = r, Prometheus, promAPI, "/api/v1/otlp/v1/metrics"
		resources = func(f func(*resource.Resource) *resource.Resource) {
			for _, x := range r.ResourceMetrics {
				x.Resource = f(x.Resource)
			}
		}
	default:
		return http.StatusNotFound, fmt.Errorf("there's no signal %q", signal)
	}
	var err error
	if isJSON {
		if body, err = hexIDs(body); err == nil {
			err = protojson.Unmarshal(body, msg)
		}
	} else {
		err = proto.Unmarshal(body, msg)
	}
	if err != nil {
		return http.StatusBadRequest, fmt.Errorf("that isn't OpenTelemetry %s: %w", signal, err)
	}
	resources(func(r *resource.Resource) *resource.Resource {
		if r == nil {
			r = &resource.Resource{}
		}
		r.Attributes = stamp(r.Attributes, map[string]string{"pier.project": project, "deployment.environment": env}, project)
		return r
	})
	if after != nil {
		after()
	}
	out, err := proto.Marshal(msg)
	if err != nil {
		return http.StatusInternalServerError, err
	}
	addr, err := m.address(ctx, target, port)
	if err != nil {
		return http.StatusServiceUnavailable, fmt.Errorf("%s isn't running: %w", target, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+path, bytes.NewReader(out))
	if err != nil {
		return http.StatusInternalServerError, err
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("X-Scope-OrgID", project)
	resp, err := m.client().Do(req)
	if err != nil {
		m.forget()
		return http.StatusBadGateway, fmt.Errorf("%s didn't answer: %w", target, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return resp.StatusCode, fmt.Errorf("%s said %d: %s", target, resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return http.StatusOK, nil
}

// stamp sets attributes on a resource, replacing any an app sent with the
// same names, and names the service after the project if the app didn't.
func stamp(attrs []*common.KeyValue, set map[string]string, project string) []*common.KeyValue {
	out := attrs[:0:0]
	hasService := false
	for _, kv := range attrs {
		if kv == nil {
			continue
		}
		if _, replaced := set[kv.Key]; replaced {
			continue
		}
		if kv.Key == "service.name" {
			hasService = true
		}
		out = append(out, kv)
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if set[k] == "" {
			continue
		}
		out = append(out, &common.KeyValue{Key: k, Value: &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: set[k]}}})
	}
	if !hasService {
		out = append(out, &common.KeyValue{Key: "service.name", Value: &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: project}}})
	}
	return out
}

// hexIDs rewrites the trace and span IDs in OTLP's JSON, which writes them
// in hex, as the base64 the protobuf JSON decoder reads.
func hexIDs(body []byte) ([]byte, error) {
	var v any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	var walk func(any)
	walk = func(x any) {
		switch t := x.(type) {
		case map[string]any:
			for k, val := range t {
				if s, ok := val.(string); ok && (k == "traceId" || k == "spanId" || k == "parentSpanId") && s != "" {
					if b, err := hex.DecodeString(s); err == nil {
						t[k] = base64.StdEncoding.EncodeToString(b)
					}
					continue
				}
				walk(val)
			}
		case []any:
			for _, val := range t {
				walk(val)
			}
		}
	}
	walk(v)
	return json.Marshal(v)
}
