package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"

	"pier/internal/store"
	"pier/internal/telemetry"
)

// grafana proxies /grafana/ to the Grafana pierd runs, for people signed in
// to the dashboard. Grafana trusts the auth proxy headers pierd sets, so it
// has no sign-in of its own: a person is the same person, with the same
// role, in both. It answers only browsers, so the CLI's tokens don't reach
// it, and anything but a read must come from the dashboard's own origin.
func (a *API) grafana() http.Handler {
	var mu sync.Mutex
	proxies := map[string]*httputil.ReverseProxy{}
	proxyTo := func(addr string) *httputil.ReverseProxy {
		mu.Lock()
		defer mu.Unlock()
		if p := proxies[addr]; p != nil {
			return p
		}
		target := &url.URL{Scheme: "http", Host: addr}
		p := &httputil.ReverseProxy{Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.SetXForwarded()
			u, _ := pr.In.Context().Value(userKey).(store.User)
			for _, h := range []string{telemetry.GrafanaUser, telemetry.GrafanaName, telemetry.GrafanaEmail, telemetry.GrafanaRole, "Authorization"} {
				pr.Out.Header.Del(h)
			}
			pr.Out.Header.Set(telemetry.GrafanaUser, u.Email)
			pr.Out.Header.Set(telemetry.GrafanaEmail, u.Email)
			pr.Out.Header.Set(telemetry.GrafanaName, u.DisplayName())
			pr.Out.Header.Set(telemetry.GrafanaRole, grafanaRole(u.Role))
			// Grafana needn't see Pier's own session.
			var kept []string
			for _, c := range pr.In.Cookies() {
				if c.Name != cookieName && c.Name != secureCookieName {
					kept = append(kept, c.Name+"="+c.Value)
				}
			}
			pr.Out.Header.Del("Cookie")
			if len(kept) > 0 {
				pr.Out.Header.Set("Cookie", strings.Join(kept, "; "))
			}
		}}
		proxies[addr] = p
		return p
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.Telemetry == nil {
			http.NotFound(w, r)
			return
		}
		ctx := r.Context()
		c, err := sessionCookie(r)
		var u store.User
		ok := false
		if err == nil {
			u, _, ok = a.lookupSession(ctx, c.Value, r)
		}
		if !ok || u.Email == "" {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-Pier") == "" {
			scheme := "http"
			if secure(r) {
				scheme = "https"
			}
			if r.Header.Get("Origin") != scheme+"://"+r.Host {
				http.Error(w, "Grafana takes changes only from this dashboard's own pages.", http.StatusForbidden)
				return
			}
		}
		addr, err := a.Telemetry.GrafanaAddress(ctx)
		if err != nil {
			msg := "Grafana isn't running. Give it a minute after turning traces, metrics and app logs on."
			if errors.Is(err, telemetry.ErrOff) {
				msg = "Grafana runs while traces, metrics and app logs are on. The owner turns them on in Server."
			}
			http.Error(w, msg, http.StatusServiceUnavailable)
			return
		}
		proxyTo(addr).ServeHTTP(w, r.WithContext(context.WithValue(ctx, userKey, u)))
	})
}

func grafanaRole(role string) string {
	switch role {
	case store.RoleOwner:
		return "Admin"
	case store.RoleMember:
		return "Editor"
	}
	return "Viewer"
}

// projectGrafana is GET /api/projects/{project}/grafana: the project's data
// sources in Grafana, for links into its Explore.
func (a *API) projectGrafana(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	if _, ok := a.findProject(w, r, name); !ok {
		return
	}
	on := a.Telemetry != nil && a.Telemetry.On(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"on": on, "path": "/grafana/", "traces": telemetry.TracesUID(name), "logs": telemetry.LogsUID(name), "metrics": telemetry.MetricsUID})
}
