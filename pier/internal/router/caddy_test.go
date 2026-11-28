package router

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/caddyserver/caddy/v2"
)

// built is the part of a generated config these tests read.
type built struct {
	Apps struct {
		HTTP struct {
			Servers map[string]struct {
				ConnPolicies []struct {
					DefaultSNI string `json:"default_sni"`
				} `json:"tls_connection_policies"`
				Routes []struct {
					Match []struct {
						Host []string `json:"host"`
					} `json:"match"`
					Handle []struct {
						Handler          string `json:"handler"`
						StreamCloseDelay string `json:"stream_close_delay"`
						Upstreams        []struct {
							Dial string `json:"dial"`
						} `json:"upstreams"`
					} `json:"handle"`
				} `json:"routes"`
			} `json:"servers"`
		} `json:"http"`
		TLS struct {
			Automation struct {
				Policies []struct {
					Subjects []string `json:"subjects"`
				} `json:"policies"`
			} `json:"automation"`
		} `json:"tls"`
	} `json:"apps"`
}

// build generates the config and has Caddy itself check it, as it does when
// pierd loads one.
func build(t *testing.T, c Caddy, routes []Route) built {
	t.Helper()
	raw, err := c.Config(routes)
	if err != nil {
		t.Fatal(err)
	}
	if err := validate(raw); err != nil {
		t.Fatalf("Caddy refuses the config: %v\n%s", err, raw)
	}
	var cfg built
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func validate(raw []byte) error {
	var cfg caddy.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return err
	}
	return caddy.Validate(&cfg)
}

// Pier loads a new config on every deploy of any project. Without a delay
// that closes every WebSocket of every application and of the dashboard.
func TestProxiesKeepStreamsOpenAcrossReloads(t *testing.T) {
	cfg := build(t, Caddy{Listen: []string{":8088"}, Storage: t.TempDir()}, []Route{
		{Host: "chat.pier.localhost", Upstream: "127.0.0.1:40001", DeploymentID: "c19e77"},
		{Host: "pier.localhost", Upstream: "127.0.0.1:7777"},
		{Host: "blog.pier.localhost", Root: "/data/deployments/8f3a2c", DeploymentID: "8f3a2c"},
		{Host: "feat--chat.pier.localhost", Wake: "127.0.0.1:41234", DeploymentID: "d00001"},
	})
	proxies := 0
	for _, r := range cfg.Apps.HTTP.Servers["pier"].Routes {
		for _, h := range r.Handle {
			if h.Handler != "reverse_proxy" {
				continue
			}
			proxies++
			if h.StreamCloseDelay != "5m" {
				t.Errorf("the proxy for %v closes its streams on reload: stream_close_delay = %q", r.Match[0].Host, h.StreamCloseDelay)
			}
		}
	}
	if proxies != 3 {
		t.Fatalf("want the application's, the waker's and the dashboard's proxies, got %d", proxies)
	}
}

// A sleeping application's address goes to pierd's waker, naming the
// deployment to wake in a header a visitor can't set for it.
func TestSleepingApplicationsGoToTheWaker(t *testing.T) {
	c := Caddy{Listen: []string{":8088"}, Storage: t.TempDir()}
	build(t, c, []Route{{Host: "feat--chat.pier.localhost", Wake: "127.0.0.1:41234", DeploymentID: "d00001"}})
	raw, err := c.Config([]Route{{Host: "feat--chat.pier.localhost", Wake: "127.0.0.1:41234", DeploymentID: "d00001"}})
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Apps struct {
			HTTP struct {
				Servers map[string]struct {
					Routes []struct {
						Handle []struct {
							Upstreams []struct{ Dial string } `json:"upstreams"`
							Headers   struct {
								Request  struct{ Set map[string][]string } `json:"request"`
								Response struct{ Set map[string][]string } `json:"response"`
							} `json:"headers"`
						} `json:"handle"`
					} `json:"routes"`
				} `json:"servers"`
			} `json:"http"`
		} `json:"apps"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	h := cfg.Apps.HTTP.Servers["pier"].Routes[0].Handle[0]
	if len(h.Upstreams) != 1 || h.Upstreams[0].Dial != "127.0.0.1:41234" {
		t.Fatalf("upstreams = %+v", h.Upstreams)
	}
	if got := h.Headers.Request.Set[WakeHeader]; !slices.Equal(got, []string{"d00001"}) {
		t.Fatalf("%s = %v", WakeHeader, got)
	}
	if got := h.Headers.Response.Set["X-Pier-Deployment"]; !slices.Equal(got, []string{"d00001"}) {
		t.Fatalf("the request log can't tell which deployment answered: %v", got)
	}
}

// Two things can claim one address: a custom domain under a base domain
// that was saved later, or a project named like another's deployment. Caddy
// refuses a config that names a host twice, and at startup pierd would exit
// with every site down, so the first route given for a host is the one kept.
func TestDuplicateHostsKeepTheFirstRoute(t *testing.T) {
	c := Caddy{HTTPS: true, Listen: []string{":443"}, Storage: t.TempDir(), BaseDomain: "new.com"}
	routes := []Route{
		{Host: "blog.new.com", Upstream: "127.0.0.1:40001", DeploymentID: "aaaaaa"},
		{Host: "blog-c19e77.new.com", Upstream: "127.0.0.1:40002", DeploymentID: "bbbbbb"},
		{Host: "docs.owner.example.org", Upstream: "127.0.0.1:40003", DeploymentID: "cccccc"},
		// The same addresses again, as a custom domain and a deployment's own.
		{Host: "blog.new.com", Upstream: "127.0.0.1:40004", DeploymentID: "dddddd"},
		{Host: "Blog-C19E77.new.com", Upstream: "127.0.0.1:40005", DeploymentID: "c19e77"},
		{Host: "docs.owner.example.org", Redirect: "https://elsewhere.example.org"},
	}
	if err := validate(mustConfig(t, c, routes[:3])); err != nil {
		t.Fatalf("Caddy refuses distinct hosts: %v", err)
	}
	twice, _ := json.Marshal(map[string]any{"apps": map[string]any{"tls": c.tlsConfig(routes)}})
	if err := validate(twice); err == nil || !strings.Contains(err.Error(), "more than one automation policy to host") {
		t.Fatalf("Caddy no longer refuses a host named twice, which is why Config drops duplicates: %v", err)
	}

	cfg := build(t, c, routes)
	dial := map[string]string{}
	for _, r := range cfg.Apps.HTTP.Servers["pier"].Routes {
		// The catch-all routes name no host: one matches browsers by Accept.
		if len(r.Match) == 0 || len(r.Match[0].Host) == 0 {
			continue
		}
		host := r.Match[0].Host[0]
		if _, dup := dial[host]; dup || len(r.Handle[0].Upstreams) != 1 {
			t.Fatalf("%s has a second route, or one that isn't the first given: %+v", host, r)
		}
		dial[host] = r.Handle[0].Upstreams[0].Dial
	}
	want := map[string]string{"blog.new.com": "127.0.0.1:40001", "blog-c19e77.new.com": "127.0.0.1:40002", "docs.owner.example.org": "127.0.0.1:40003"}
	if len(dial) != len(want) {
		t.Fatalf("routes = %v, want %v", dial, want)
	}
	for host, upstream := range want {
		if dial[host] != upstream {
			t.Errorf("%s goes to %q, want the first route's %s", host, dial[host], upstream)
		}
	}
	var subjects []string
	for _, p := range cfg.Apps.TLS.Automation.Policies {
		subjects = append(subjects, p.Subjects...)
	}
	slices.Sort(subjects)
	if !slices.Equal(subjects, []string{"blog-c19e77.new.com", "blog.new.com", "docs.owner.example.org"}) {
		t.Fatalf("certificate subjects = %v", subjects)
	}
}

func mustConfig(t *testing.T, c Caddy, routes []Route) []byte {
	t.Helper()
	raw, err := c.Config(routes)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// A browser sends no server name for https://203.0.113.10, and behind NAT
// the address the connection arrives on is private, so no certificate would
// match without a default name.
func TestIPDashboardNamesItsCertificate(t *testing.T) {
	c := Caddy{HTTPS: true, Listen: []string{":443"}, Storage: t.TempDir(), BaseDomain: "pier.example.com"}
	site := Route{Host: "blog.pier.example.com", Root: "/data/deployments/8f3a2c", DeploymentID: "8f3a2c"}
	cfg := build(t, c, []Route{site, {Host: "203.0.113.10", Upstream: "127.0.0.1:7777"}})
	policies := cfg.Apps.HTTP.Servers["pier"].ConnPolicies
	if len(policies) != 1 || policies[0].DefaultSNI != "203.0.113.10" {
		t.Fatalf("connection policies = %+v, want the public IP as the default server name", policies)
	}

	// A dashboard reached by name is asked for by name.
	cfg = build(t, c, []Route{site, {Host: "pier.example.com", Upstream: "127.0.0.1:7777"}})
	if policies := cfg.Apps.HTTP.Servers["pier"].ConnPolicies; len(policies) != 0 {
		t.Fatalf("connection policies for a named dashboard = %+v", policies)
	}
}

func TestConfig(t *testing.T) {
	c := Caddy{Admin: "http://127.0.0.1:2019", Listen: []string{":8088"}}
	raw, err := c.Config([]Route{
		{Host: "feat-search--blog.pier.localhost", Root: "/data/deployments/c19e77", DeploymentID: "c19e77"},
		{Host: "blog.pier.localhost", Root: "/data/deployments/8f3a2c", DeploymentID: "8f3a2c"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Admin struct{ Listen string } `json:"admin"`
		Apps  struct {
			HTTP struct {
				Servers map[string]struct {
					Listen         []string `json:"listen"`
					AutomaticHTTPS struct {
						Disable bool `json:"disable"`
					} `json:"automatic_https"`
					Routes []struct {
						Match []struct {
							Host []string `json:"host"`
						} `json:"match"`
					} `json:"routes"`
				} `json:"servers"`
			} `json:"http"`
		} `json:"apps"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Admin.Listen != "127.0.0.1:2019" {
		t.Errorf("admin listen = %q", cfg.Admin.Listen)
	}
	s := cfg.Apps.HTTP.Servers["pier"]
	if !s.AutomaticHTTPS.Disable || s.Listen[0] != ":8088" {
		t.Errorf("server = %+v", s)
	}
	if len(s.Routes) != 4 {
		t.Fatalf("want 2 sites and the two catch-all 404s, a page and a line, got %d routes", len(s.Routes))
	}
	if h := s.Routes[0].Match[0].Host[0]; h != "blog.pier.localhost" {
		t.Errorf("routes not sorted by host: first is %q", h)
	}
	if len(s.Routes[3].Match) != 0 {
		t.Error("the last route should be the catch-all 404")
	}
	if !strings.Contains(string(raw), `"X-Pier-Deployment"`) {
		t.Error("deployment header missing")
	}
}

func TestApply(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/load" || r.Method != http.MethodPost {
			http.Error(w, "wrong endpoint", http.StatusNotFound)
			return
		}
		b, _ := io.ReadAll(r.Body)
		got = string(b)
	}))
	defer srv.Close()
	c := Caddy{Admin: srv.URL, Listen: []string{":8088"}}
	if err := c.Apply(context.Background(), []Route{{Host: "blog.pier.localhost", Root: "/x", DeploymentID: "8f3a2c"}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "blog.pier.localhost") {
		t.Fatal("config wasn't sent")
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"bad config"}`, http.StatusBadRequest)
	}))
	defer bad.Close()
	if err := (Caddy{Admin: bad.URL}).Apply(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "bad config") {
		t.Fatalf("want Caddy's error passed on, got %v", err)
	}
}

// An address nothing is deployed at gets a page in a browser and one plain
// line anywhere else. Caddy expands {placeholders} in a response body, and the
// page is full of braces, so it must arrive exactly as written.
func TestNothingDeployedPage(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	c := Caddy{Listen: []string{addr}, Storage: t.TempDir()}
	if err := c.Apply(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Stop() })
	get := func(accept string) (int, string, string) {
		t.Helper()
		req, _ := http.NewRequest("GET", "http://"+addr+"/some/page", nil)
		req.Host = "gone.pier.localhost"
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header.Get("Content-Type"), string(b)
	}
	code, kind, body := get("text/html,application/xhtml+xml,*/*;q=0.8")
	if code != 404 || !strings.HasPrefix(kind, "text/html") || body != notFoundPage {
		t.Fatalf("a browser got %d %s, and the page changed on the way: %v", code, kind, body != notFoundPage)
	}
	if strings.Contains(body, "{{") || !strings.Contains(body, "Nothing is deployed here.") {
		t.Fatal("the page's fonts or words are missing")
	}
	if code, kind, body := get(""); code != 404 || !strings.HasPrefix(kind, "text/plain") || body != notFoundText {
		t.Fatalf("curl got %d %s %q", code, kind, body)
	}
}

// With an events listener, every deployment's address sends /_pier/event to
// it with the deployment named, and everything else as before; redirects
// and the dashboard don't.
func TestEventsGoToPierd(t *testing.T) {
	c := Caddy{Listen: []string{":8088"}, Storage: t.TempDir(), Events: "127.0.0.1:41999"}
	raw := mustConfig(t, c, []Route{
		{Host: "shop.pier.localhost", Root: t.TempDir(), DeploymentID: "d00001"},
		{Host: "www.shop.example.com", Redirect: "https://shop.example.com"},
		{Host: "pier.localhost", Upstream: "127.0.0.1:41000"},
	})
	if err := validate(raw); err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	json.Compact(&compact, raw)
	s := compact.String()
	if strings.Count(s, `"/_pier/event"`) != 1 || !strings.Contains(s, `"X-Pier-Event":["d00001"]`) || !strings.Contains(s, `"dial":"127.0.0.1:41999"`) {
		t.Fatalf("the deployment's events don't reach pierd:\n%s", s)
	}
}
