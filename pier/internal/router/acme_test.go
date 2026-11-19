package router

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddytls"
	"github.com/miekg/dns"
	"pier/internal/challengedns"
	"pier/internal/dnscheck"
)

// TestLocalACME exercises issuance with Pebble doing real challenge validation.
// Its CA, resolver, challenge responder and web servers bind only to loopback.
// PIER_TEST_PEBBLE is the executable, PIER_TEST_PEBBLE_CERTS is Pebble's test/certs.
func TestLocalACME(t *testing.T) {
	bin := os.Getenv("PIER_TEST_PEBBLE")
	if bin == "" {
		t.Skip("set PIER_TEST_PEBBLE and PIER_TEST_PEBBLE_CERTS to run the private ACME test")
	}
	certs := os.Getenv("PIER_TEST_PEBBLE_CERTS")
	free := func() int {
		t.Helper()
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		return ln.Addr().(*net.TCPAddr).Port
	}
	addr := func(p int) string { return net.JoinHostPort("127.0.0.1", strconv.Itoa(p)) }
	site, httpPort, ca, mgmt := free(), free(), free(), free()
	base := "pier.example.net"
	ns := "ns." + base
	zone := "_acme-challenge." + base + "."
	authAddr := addr(free())
	auth, err := challengedns.Start(base, ns, authAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer auth.Close()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	resolver := &dns.Server{PacketConn: pc, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
		q := r.Question[0]
		m := new(dns.Msg)
		m.SetReply(r)
		if strings.EqualFold(q.Name, zone) {
			var e error
			m, _, e = new(dns.Client).Exchange(r, authAddr)
			if e != nil {
				return
			}
		} else if q.Qtype == dns.TypeA {
			m.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: q.Name, Class: dns.ClassINET, Rrtype: dns.TypeA}, A: net.IPv4(127, 0, 0, 1)}}
		}
		w.WriteMsg(m)
	})}
	tcp, err := net.Listen("tcp", pc.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	streamResolver := &dns.Server{Listener: tcp, Handler: resolver.Handler}
	go streamResolver.ActivateAndServe()
	defer streamResolver.Shutdown()
	go resolver.ActivateAndServe()
	defer resolver.Shutdown()
	priorResolvers := dnscheck.Resolvers
	dnscheck.Resolvers = []string{pc.LocalAddr().String()}
	defer func() { dnscheck.Resolvers = priorResolvers }()
	if !dnscheck.ChallengeAuthority(context.Background(), base, ns) {
		t.Fatal("delegation did not lead to the challenge responder")
	}
	dir := t.TempDir()
	siteRoot := filepath.Join(dir, "site")
	if err = os.Mkdir(siteRoot, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(siteRoot, "index.html"), []byte("published build"), 0600); err != nil {
		t.Fatal(err)
	}
	write := func(path string, v any) {
		t.Helper()
		b, e := json.Marshal(v)
		if e == nil {
			e = os.WriteFile(path, b, 0600)
		}
		if e != nil {
			t.Fatal(e)
		}
	}
	cfgPath := filepath.Join(dir, "pebble.json")
	write(cfgPath, map[string]any{"pebble": map[string]any{"listenAddress": addr(ca), "managementListenAddress": addr(mgmt), "certificate": filepath.Join(certs, "localhost/cert.pem"), "privateKey": filepath.Join(certs, "localhost/key.pem"), "httpPort": httpPort, "tlsPort": site, "keyAlgorithm": "ecdsa"}})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-config", cfgPath, "-dnsserver", pc.LocalAddr().String())
	cmd.Env = append(os.Environ(), "PEBBLE_VA_NOSLEEP=1", "PEBBLE_WFE_NONCEREJECT=0", "PEBBLE_VA_ALWAYS_VALID=0")
	logPath := filepath.Join(dir, "ca.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cancel()
		cmd.Wait()
		if t.Failed() {
			b, _ := os.ReadFile(logPath)
			t.Log(string(b))
		}
	}()
	apiRoot := filepath.Join(certs, "pebble.minica.pem")
	pem, err := os.ReadFile(apiRoot)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		t.Fatal("invalid Pebble test root")
	}
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
	defer client.CloseIdleConnections()
	var issuedRoots *x509.CertPool
	for n := 0; n < 50; n++ {
		resp, e := client.Get(fmt.Sprintf("https://localhost:%d/roots/0", mgmt))
		if e == nil {
			p, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			issuedRoots = x509.NewCertPool()
			if issuedRoots.AppendCertsFromPEM(p) {
				break
			}
			issuedRoots = nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	if issuedRoots == nil {
		t.Fatal("private CA did not start")
	}
	const custom = "docs.owner.example.org"
	generated := "blog." + base
	fallback := "first." + base
	r := Caddy{HTTPS: true, Listen: []string{addr(site)}, Storage: filepath.Join(dir, "caddy"), BaseDomain: base, WildcardReady: func() bool { return true }, Resolvers: []string{pc.LocalAddr().String()}, ACMECA: fmt.Sprintf("https://localhost:%d/dir", ca)}
	raw, err := r.Config([]Route{{Host: custom, Root: siteRoot, DeploymentID: "prod"}, {Host: generated, Root: siteRoot, DeploymentID: "prod"}, {Host: fallback, Root: siteRoot, DeploymentID: "prod"}})
	if err != nil {
		t.Fatal(err)
	}
	// Only the fixture changes the trust anchors and standard challenge ports.
	var cfg map[string]any
	if err = json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	apps := cfg["apps"].(map[string]any)
	h := apps["http"].(map[string]any)
	h["http_port"] = httpPort
	h["https_port"] = site
	for _, p := range apps["tls"].(map[string]any)["automation"].(map[string]any)["policies"].([]any) {
		i := p.(map[string]any)["issuers"].([]any)[0].(map[string]any)
		i["trusted_roots_pem_files"] = []string{apiRoot}
		i["test_ca"] = r.ACMECA
		if challenges, ok := i["challenges"].(map[string]any); ok {
			challenges["dns"].(map[string]any)["propagation_timeout"] = -1
		}
	}
	raw, err = json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// With no delegated zone, a known preview can obtain a certificate on
	// first visit. A hostname outside this configuration must fail.
	tlsApp := apps["tls"].(map[string]any)
	delete(tlsApp, "certificates")
	automation := tlsApp["automation"].(map[string]any)
	policies := automation["policies"].([]any)
	automation["policies"] = policies[:len(policies)-1]
	withoutWildcard, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = caddy.Load(withoutWildcard, false); err != nil {
		t.Fatal(err)
	}
	defer r.Stop()
	if conn, err := tls.Dial("tcp", addr(site), &tls.Config{ServerName: "unconfigured.example.org", RootCAs: issuedRoots}); err == nil {
		conn.Close()
		t.Fatal("accepted an unconfigured hostname")
	}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 15 * time.Second}, "tcp", addr(site), &tls.Config{ServerName: fallback, RootCAs: issuedRoots})
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if err = caddy.Load(raw, false); err != nil {
		t.Fatal(err)
	}
	for len(caddytls.AllMatchingCertificates("*."+base)) == 0 || len(caddytls.AllMatchingCertificates(custom)) == 0 {
		select {
		case <-ctx.Done():
			t.Fatal("certificates not issued before timeout")
		case <-time.After(150 * time.Millisecond):
		}
	}
	for _, host := range []string{generated, custom} {
		conn, err := tls.Dial("tcp", addr(site), &tls.Config{ServerName: host, RootCAs: issuedRoots})
		if err != nil {
			t.Fatal(err)
		}
		leaf := conn.ConnectionState().PeerCertificates[0]
		conn.Close()
		transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: issuedRoots}, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr(site))
		}}
		browser := http.Client{Transport: transport, Timeout: 3 * time.Second}
		resp, err := browser.Get("https://" + host + "/")
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		transport.CloseIdleConnections()
		if err != nil || string(body) != "published build" || resp.Header.Get("X-Pier-Deployment") != "prod" {
			t.Fatalf("wrong deployment at %s", host)
		}
		if host == generated && leaf.DNSNames[0] != "*."+base {
			t.Fatalf("preview did not use wildcard: %v", leaf.DNSNames)
		}
	}
}
