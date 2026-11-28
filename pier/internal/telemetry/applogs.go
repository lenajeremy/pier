package telemetry

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	coll "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	logs "go.opentelemetry.io/proto/otlp/logs/v1"
	resource "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"

	"pier/internal/apphost"
)

// App is one running app container whose output goes to Loki.
type App struct {
	Container, Project, Env, Deployment string
}

// The follower's pace: how often it looks for new containers, and how
// often and how much it sends.
var (
	lookEvery  = 10 * time.Second
	sendEvery  = 2 * time.Second
	sendAtMost = 500
)

const maxLine = 64 << 10

// FollowLogs sends what every running app prints to Loki, while the stack
// is on, until ctx ends. Apps and OpenLogs must be set.
func (m *Manager) FollowLogs(ctx context.Context) {
	var mu sync.Mutex
	var wg sync.WaitGroup
	defer wg.Wait()
	following := map[string]context.CancelFunc{}
	last := map[string]time.Time{} // container -> its latest line sent
	first := true
	stopAll := func() {
		mu.Lock()
		for name, stop := range following {
			stop()
			delete(following, name)
		}
		mu.Unlock()
	}
	defer stopAll()
	tick := time.NewTicker(lookEvery)
	defer tick.Stop()
	for {
		if !m.On(ctx) {
			stopAll()
			first = true
		} else if apps, err := m.Apps(ctx); err != nil {
			m.Log.Debug("telemetry: list apps", "err", err)
		} else {
			for _, app := range apps {
				mu.Lock()
				_, on := following[app.Container]
				since, seen := last[app.Container]
				mu.Unlock()
				if on {
					continue
				}
				// Containers running when following starts send from now;
				// ones that start later send from their first line.
				from := ""
				switch {
				case seen:
					from = dockerTime(since.Add(time.Nanosecond))
				case first:
					from = dockerTime(time.Now())
				}
				fctx, stop := context.WithCancel(ctx)
				mu.Lock()
				following[app.Container] = stop
				mu.Unlock()
				wg.Add(1)
				go func(app App) {
					defer wg.Done()
					at := m.follow(fctx, app, from)
					mu.Lock()
					if !at.IsZero() {
						last[app.Container] = at
					}
					delete(following, app.Container)
					mu.Unlock()
					stop()
				}(app)
			}
			first = false
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func dockerTime(t time.Time) string { return fmt.Sprintf("%d.%09d", t.Unix(), t.Nanosecond()) }

// follow sends one container's output until it stops or ctx ends, and
// says when its latest line was.
func (m *Manager) follow(ctx context.Context, app App, since string) time.Time {
	rc, tty, err := m.OpenLogs(ctx, app.Container, since)
	if err != nil {
		m.Log.Debug("telemetry: open logs", "container", app.Container, "err", err)
		return time.Time{}
	}
	defer rc.Close()
	var mu sync.Mutex
	var batch []*logs.LogRecord
	var latest time.Time
	send := func() {
		mu.Lock()
		out := batch
		batch = nil
		mu.Unlock()
		if len(out) == 0 {
			return
		}
		if err := m.sendLogs(context.WithoutCancel(ctx), app, out); err != nil {
			m.Log.Debug("telemetry: app logs", "project", app.Project, "err", err)
		}
	}
	done := make(chan struct{})
	every := sendEvery
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				send()
			}
		}
	}()
	apphost.FollowLogLines(rc, tty, func(l apphost.LogLine) error {
		r := record(l)
		mu.Lock()
		batch = append(batch, r)
		full := len(batch) >= sendAtMost
		latest = time.Unix(0, int64(r.TimeUnixNano))
		mu.Unlock()
		if full {
			send()
		}
		return nil
	})
	close(done)
	send()
	return latest
}

// record turns one line of output into a log record. A JSON line that
// names its trace is linked to it.
func record(l apphost.LogLine) *logs.LogRecord {
	at := time.Now()
	if t, err := time.Parse(time.RFC3339Nano, l.At); err == nil {
		at = t
	}
	text := l.Text
	if len(text) > maxLine {
		text = text[:maxLine]
	}
	r := &logs.LogRecord{
		TimeUnixNano: uint64(at.UnixNano()), ObservedTimeUnixNano: uint64(time.Now().UnixNano()),
		Body:       &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: text}},
		Attributes: []*common.KeyValue{str("log.iostream", l.Stream)},
	}
	if strings.HasPrefix(strings.TrimSpace(text), "{") {
		var fields map[string]any
		if json.Unmarshal([]byte(text), &fields) == nil {
			for _, k := range []string{"trace_id", "traceId", "traceID", "trace"} {
				if s, ok := fields[k].(string); ok && len(s) == 32 {
					if id, err := hex.DecodeString(s); err == nil {
						r.TraceId = id
						break
					}
				}
			}
			for _, k := range []string{"level", "severity", "lvl"} {
				if s, ok := fields[k].(string); ok && s != "" {
					r.SeverityText = strings.ToLower(s)
					break
				}
			}
		}
	}
	return r
}

func (m *Manager) sendLogs(ctx context.Context, app App, records []*logs.LogRecord) error {
	res := &resource.Resource{Attributes: []*common.KeyValue{
		str("service.name", app.Project), str("pier.project", app.Project), str("pier.deployment", app.Deployment),
	}}
	if app.Env != "" {
		res.Attributes = append(res.Attributes, str("deployment.environment", app.Env))
	}
	return m.postLogs(ctx, app.Project, []*logs.ResourceLogs{{
		Resource:  res,
		ScopeLogs: []*logs.ScopeLogs{{Scope: &common.InstrumentationScope{Name: "pier.stdout"}, LogRecords: records}},
	}})
}

// postLogs hands log records to Loki, as the project.
func (m *Manager) postLogs(ctx context.Context, project string, batch []*logs.ResourceLogs) error {
	body, err := proto.Marshal(&coll.ExportLogsServiceRequest{ResourceLogs: batch})
	if err != nil {
		return err
	}
	addr, err := m.address(ctx, Loki, lokiAPI)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/otlp/v1/logs", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("X-Scope-OrgID", project)
	resp, err := m.client().Do(req)
	if err != nil {
		m.forget()
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("loki said %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}
