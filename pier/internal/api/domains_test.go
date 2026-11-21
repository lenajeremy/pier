package api

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
	"pier/internal/deploy"
	"pier/internal/dnscheck"
	"pier/internal/router"
	"pier/internal/store"
)

func TestCustomDomainLifecycle(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "pier.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	for _, p := range []string{"blog", "docs"} {
		if err := st.EnsureProject(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetSetting(ctx, store.SettingPublicIP, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	var correct atomic.Bool
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &dns.Server{PacketConn: pc, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(req)
		for _, q := range req.Question {
			if correct.Load() && q.Name == "www.example.com." && q.Qtype == dns.TypeCNAME {
				m.Answer = append(m.Answer, &dns.CNAME{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeCNAME, Class: dns.ClassINET}, Target: "example.com."})
			}
			if q.Name == "example.com." && q.Qtype == dns.TypeA {
				ip := "192.0.2.1"
				if correct.Load() {
					ip = "127.0.0.1"
				}
				m.Answer = append(m.Answer, &dns.A{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET}, A: net.ParseIP(ip)})
			}
		}
		w.WriteMsg(m)
	})}
	go server.ActivateAndServe()
	defer server.Shutdown()
	old := dnscheck.Resolvers
	dnscheck.Resolvers = []string{pc.LocalAddr().String()}
	defer func() { dnscheck.Resolvers = old }()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	dep := &deploy.Deployer{Store: st, Domain: "pier.localhost", PierHost: "pier.localhost", Deployments: root, ProductionBranch: "main", URL: func(host string) string { return "http://" + host }, Router: router.Caddy{Listen: []string{addr}, Storage: filepath.Join(root, "caddy")}}
	a := &API{Store: st, Token: "fixture", Deployer: dep, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	api := httptest.NewServer(a.Public())
	defer api.Close()
	call := func(method, path, body string, want int) string {
		t.Helper()
		req, _ := http.NewRequest(method, api.URL+"/api/projects/"+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer fixture")
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != want {
			t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, b)
		}
		return string(b)
	}
	serve := func(id string) {
		t.Helper()
		d := store.Deployment{ID: id, Project: "blog", Target: store.TargetProduction, Branch: "main", Status: store.Ready}
		if err := st.CreateDeployment(ctx, d); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(root, id), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, id, "index.html"), []byte(id), 0600); err != nil {
			t.Fatal(err)
		}
		if err := st.SetAlias(ctx, store.Alias{Host: "blog.pier.localhost", Project: "blog", Kind: store.KindProduction, DeploymentID: id, UpdatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
		if err := dep.Sync(ctx); err != nil {
			t.Fatal(err)
		}
	}
	serve("000001")
	defer dep.Router.Stop()
	fetch := func(host, path string, want int) string {
		t.Helper()
		req, _ := http.NewRequest("GET", "http://"+addr+path, nil)
		req.Host = host
		c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("%s %s: %d", host, path, resp.StatusCode)
		}
		if want == 308 {
			return resp.Header.Get("Location")
		}
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	call("POST", "blog/domains", `{"host":"blog.pier.localhost"}`, 400)
	call("POST", "blog/domains", `{"host":"ExAmPle.com."}`, 201)
	call("POST", "docs/domains", `{"host":"www.example.com"}`, 409)
	call("POST", "blog/domains/example.com/check", "", 200)
	if s := call("GET", "blog/domains", "", 200); !strings.Contains(s, "192.0.2.1") || !strings.Contains(s, "Not found yet") {
		t.Fatal(s)
	}
	fetch("example.com", "/", 404)
	correct.Store(true)
	call("POST", "blog/domains/example.com/check", "", 200)
	if body := fetch("example.com", "/", 200); body != "000001" {
		t.Fatal(body)
	}
	if loc := fetch("www.example.com", "/guide?q=one", 308); loc != "http://example.com/guide?q=one" {
		t.Fatal(loc)
	}
	serve("000002")
	if body := fetch("example.com", "/", 200); body != "000002" {
		t.Fatal(body)
	}
	call("DELETE", "docs/domains/example.com", "", 404)
	call("DELETE", "blog/domains/example.com", "", 200)
	fetch("example.com", "/", 404)
	fetch("www.example.com", "/", 404)
}

// A base domain the owner saved applies at the next restart. A custom domain
// under it would then be both a project's own address and a custom domain,
// which the router can't serve twice.
func TestCustomDomainCannotBeUnderASavedBaseDomain(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "pier.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.EnsureProject(ctx, "blog"); err != nil {
		t.Fatal(err)
	}
	dep := &deploy.Deployer{Store: st, Domain: "pier.localhost", PierHost: "pier.localhost"}
	a := &API{Store: st, Token: "fixture", Deployer: dep, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	api := httptest.NewServer(a.Public())
	defer api.Close()
	add := func(host string, want int) {
		t.Helper()
		req, _ := http.NewRequest("POST", api.URL+"/api/projects/blog/domains", strings.NewReader(`{"host":"`+host+`"}`))
		req.Header.Set("Authorization", "Bearer fixture")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if b, _ := io.ReadAll(resp.Body); resp.StatusCode != want {
			t.Fatalf("adding %s: %d %s, want %d", host, resp.StatusCode, b, want)
		}
	}

	add("blog.new.com", 201)
	if err := st.RemoveDomain(ctx, "blog", "blog.new.com"); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveAddresses(ctx, "dash.owner.example.org", "new.com"); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"blog.new.com", "new.com", "dash.owner.example.org", "blog.pier.localhost"} {
		add(host, 400)
	}
	add("blog.example.com", 201)
}
