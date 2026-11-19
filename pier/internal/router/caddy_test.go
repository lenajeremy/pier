package router

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
	if len(s.Routes) != 3 {
		t.Fatalf("want 2 sites and a fallback, got %d routes", len(s.Routes))
	}
	if h := s.Routes[0].Match[0].Host[0]; h != "blog.pier.localhost" {
		t.Errorf("routes not sorted by host: first is %q", h)
	}
	if len(s.Routes[2].Match) != 0 {
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
