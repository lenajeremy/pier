package telemetry

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"pier/internal/apphost"
)

// Grafana runs beside the stack for anyone who wants Grafana's own views.
// pierd signs people in: it proxies /grafana/ to it with the signed-in
// person in Grafana's auth proxy headers, so Grafana has no passwords of its
// own. Its data sources are pierd's to write: one traces and one logs source
// per project, each sending the project's tenant, and the metrics.

const (
	grafanaPort     = 3000
	settingGrafana  = "grafana_password"
	grafanaDSHeader = "X-Scope-OrgID"
	// MetricsUID names the metrics data source in Grafana.
	MetricsUID = "pier-metrics"
)

// The headers pierd sends Grafana with the signed-in person.
const (
	GrafanaUser  = "X-Pier-User"
	GrafanaName  = "X-Pier-Name"
	GrafanaEmail = "X-Pier-Email"
	GrafanaRole  = "X-Pier-Role"
)

func uid(kind, project string) string {
	id := "pier-" + kind + "-" + project
	if len(id) > 40 {
		sum := sha256.Sum256([]byte(project))
		id = "pier-" + kind + "-" + hex.EncodeToString(sum[:12])
	}
	return id
}

// TracesUID names a project's traces data source in Grafana.
func TracesUID(project string) string { return uid("t", project) }

// LogsUID names a project's logs data source in Grafana.
func LogsUID(project string) string { return uid("l", project) }

func (m *Manager) grafanaPassword(ctx context.Context) (string, error) {
	v, err := m.Store.Setting(ctx, settingGrafana)
	if err != nil || v != "" {
		return v, err
	}
	b := make([]byte, 24)
	rand.Read(b)
	v = hex.EncodeToString(b)
	if err := m.Store.SetSetting(ctx, settingGrafana, v); err != nil {
		return "", err
	}
	return v, nil
}

func (m *Manager) grafanaDir() string { return filepath.Join(m.Dir, "grafana", "datasources") }

func (m *Manager) grafanaSpec(ctx context.Context) (apphost.ServiceSpec, error) {
	pw, err := m.grafanaPassword(ctx)
	if err != nil {
		return apphost.ServiceSpec{}, err
	}
	sources, _, err := m.writeDatasources(ctx)
	if err != nil {
		return apphost.ServiceSpec{}, err
	}
	root := m.GrafanaURL
	if root == "" {
		root = "http://localhost/grafana/"
	}
	env := []string{
		"GF_SERVER_ROOT_URL=" + root,
		"GF_SERVER_SERVE_FROM_SUB_PATH=true",
		"GF_AUTH_PROXY_ENABLED=true",
		"GF_AUTH_PROXY_HEADER_NAME=" + GrafanaUser,
		"GF_AUTH_PROXY_HEADER_PROPERTY=username",
		"GF_AUTH_PROXY_AUTO_SIGN_UP=true",
		"GF_AUTH_PROXY_HEADERS=Name:" + GrafanaName + " Email:" + GrafanaEmail + " Role:" + GrafanaRole,
		"GF_AUTH_PROXY_SYNC_TTL=1",
		"GF_AUTH_DISABLE_LOGIN_FORM=true",
		"GF_AUTH_DISABLE_SIGNOUT_MENU=true",
		"GF_USERS_ALLOW_SIGN_UP=false",
		"GF_USERS_DEFAULT_THEME=dark",
		"GF_SECURITY_ADMIN_USER=pier",
		"GF_SECURITY_ADMIN_PASSWORD=" + pw,
		"GF_SECURITY_COOKIE_SAMESITE=lax",
		"GF_ANALYTICS_REPORTING_ENABLED=false",
		"GF_ANALYTICS_CHECK_FOR_UPDATES=false",
		"GF_ANALYTICS_CHECK_FOR_PLUGIN_UPDATES=false",
		"GF_NEWS_NEWS_FEED_ENABLED=false",
		"GF_PLUGINS_PREINSTALL_DISABLED=true",
		"GF_LOG_LEVEL=warn",
	}
	sum := sha256.Sum256([]byte(images[Grafana] + strings.Join(env, "\n") + "copied"))
	return apphost.ServiceSpec{
		Name: Grafana, Image: images[Grafana], Ports: []int{grafanaPort}, Env: env, Data: "/var/lib/grafana", MemoryMB: 256, Shared: true,
		Files:  map[string][]byte{datasourcesAt: sources},
		Config: hex.EncodeToString(sum[:8]),
	}, nil
}

type datasource struct {
	Name           string         `json:"name"`
	UID            string         `json:"uid"`
	Type           string         `json:"type"`
	URL            string         `json:"url"`
	Access         string         `json:"access"`
	IsDefault      bool           `json:"isDefault,omitempty"`
	Editable       bool           `json:"editable"`
	JSONData       map[string]any `json:"jsonData,omitempty"`
	SecureJSONData map[string]any `json:"secureJsonData,omitempty"`
}

// datasourcesAt is where Grafana reads the data sources pierd writes.
const datasourcesAt = "/etc/grafana/provisioning/datasources/pier.yaml"

// writeDatasources writes the data sources for every project, keeps a copy
// in pierd's folder, and says whether they changed. JSON is YAML, which
// Grafana reads.
func (m *Manager) writeDatasources(ctx context.Context) ([]byte, bool, error) {
	var projects []string
	if m.Projects != nil {
		var err error
		if projects, err = m.Projects(ctx); err != nil {
			return nil, false, err
		}
	}
	ds := []datasource{{Name: "Metrics", UID: MetricsUID, Type: "prometheus", URL: "http://prometheus:9090", Access: "proxy", IsDefault: true,
		JSONData: map[string]any{"timeInterval": "30s", "httpMethod": "POST"}}}
	for _, p := range projects {
		tenant := map[string]any{"httpHeaderValue1": p}
		ds = append(ds,
			datasource{Name: p + " traces", UID: TracesUID(p), Type: "tempo", URL: "http://tempo:3200", Access: "proxy", SecureJSONData: tenant,
				JSONData: map[string]any{
					"httpHeaderName1": grafanaDSHeader,
					"tracesToLogsV2":  map[string]any{"datasourceUid": LogsUID(p), "filterByTraceID": true, "spanStartTimeShift": "-5m", "spanEndTimeShift": "5m"},
					"serviceMap":      map[string]any{"datasourceUid": MetricsUID},
					"nodeGraph":       map[string]any{"enabled": true},
				}},
			datasource{Name: p + " logs", UID: LogsUID(p), Type: "loki", URL: "http://loki:3100", Access: "proxy", SecureJSONData: tenant,
				JSONData: map[string]any{
					"httpHeaderName1": grafanaDSHeader,
					// Grafana reads $$ as a literal $ in provisioning files.
					"derivedFields": []map[string]any{{"name": "trace", "matcherType": "label", "matcherRegex": "trace_id", "datasourceUid": TracesUID(p), "url": "$${__value.raw}"}},
				}},
		)
	}
	body, err := json.MarshalIndent(map[string]any{"apiVersion": 1, "prune": true, "datasources": ds}, "", "  ")
	if err != nil {
		return nil, false, err
	}
	if err := os.MkdirAll(m.grafanaDir(), 0o700); err != nil {
		return nil, false, err
	}
	path := filepath.Join(m.grafanaDir(), "pier.yaml")
	if old, err := os.ReadFile(path); err == nil && string(old) == string(body) {
		return body, false, nil
	}
	return body, true, os.WriteFile(path, body, 0o600)
}

var grafanaSync sync.Mutex

// SyncGrafana keeps Grafana's data sources to the projects there are.
func (m *Manager) SyncGrafana(ctx context.Context) error {
	grafanaSync.Lock()
	defer grafanaSync.Unlock()
	body, changed, err := m.writeDatasources(ctx)
	if err != nil || !changed {
		return err
	}
	if err := m.Containers.CopyFile(ctx, Grafana, datasourcesAt, body); err != nil {
		return err
	}
	addr, err := m.address(ctx, Grafana, grafanaPort)
	if err != nil {
		return err
	}
	pw, err := m.grafanaPassword(ctx)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/grafana/api/admin/provisioning/datasources/reload", nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth("pier", pw)
	resp, err := m.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("grafana said %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

// GrafanaAddress is where pierd reaches Grafana, when the stack is on.
func (m *Manager) GrafanaAddress(ctx context.Context) (string, error) {
	if !m.On(ctx) {
		return "", ErrOff
	}
	return m.address(ctx, Grafana, grafanaPort)
}
