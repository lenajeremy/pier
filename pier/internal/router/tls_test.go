package router

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/caddyserver/certmagic"
)

// keep stores a wildcard certificate where certmagic keeps the one it
// obtained from issuer.
func keep(t *testing.T, storage, issuer, base string, expires time.Time) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "*." + base}, DNSNames: []string{"*." + base},
		NotBefore: expires.Add(-90 * 24 * time.Hour), NotAfter: expires,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(storage, filepath.FromSlash(certmagic.StorageKeys.SiteCert(issuer, "*."+base)))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
}

// pierd keeps the wildcard policy through failed DNS probes for as long as
// this says a usable certificate is stored.
func TestHasWildcardCertificate(t *testing.T) {
	c := Caddy{Storage: t.TempDir(), BaseDomain: "pier.example.com"}
	if c.HasWildcardCertificate() {
		t.Fatal("found a certificate in empty storage")
	}
	keep(t, c.Storage, "acme-v02.api.letsencrypt.org-directory", "other.example.com", time.Now().Add(time.Hour))
	keep(t, c.Storage, "acme-v02.api.letsencrypt.org-directory", c.BaseDomain, time.Now().Add(-time.Hour))
	if c.HasWildcardCertificate() {
		t.Fatal("counted another domain's certificate, or an expired one")
	}
	keep(t, c.Storage, "acme-staging-v02.api.letsencrypt.org-directory", c.BaseDomain, time.Now().Add(time.Hour))
	if !c.HasWildcardCertificate() {
		t.Fatal("missed the stored wildcard certificate")
	}
}

// The public Postgres port has no route, so nothing would ask Caddy for its
// certificate: Certify does, until the wildcard covers it. A project named
// db shares the host, and Caddy refuses a host in two policies.
func TestCertifyKeepsACertificateWithoutARoute(t *testing.T) {
	routes := []Route{
		{Host: "blog.new.com", Upstream: "127.0.0.1:40001", DeploymentID: "aaaaaa"},
		{Host: "db.new.com", Upstream: "127.0.0.1:40002", DeploymentID: "bbbbbb"},
	}
	for _, c := range []struct {
		name   string
		routes []Route
		wild   bool
		want   []string
	}{
		{"alone", routes[:1], false, []string{"db.new.com"}},
		{"with a project named db", routes, false, []string{"db.new.com"}},
		{"with the wildcard", routes[:1], true, []string{"*.new.com"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			cfg := Caddy{HTTPS: true, Listen: []string{":443"}, Storage: t.TempDir(), BaseDomain: "new.com",
				Certify: []string{"db.new.com"}, WildcardReady: func() bool { return c.wild }}
			raw := mustConfig(t, cfg, c.routes)
			if err := validate(raw); err != nil {
				t.Fatalf("Caddy refuses the config: %v", err)
			}
			var got struct {
				Apps struct {
					TLS struct {
						Certificates struct {
							Automate []string `json:"automate"`
						} `json:"certificates"`
						Automation struct {
							Policies []struct {
								Subjects []string `json:"subjects"`
								OnDemand bool     `json:"on_demand"`
							} `json:"policies"`
						} `json:"automation"`
					} `json:"tls"`
				} `json:"apps"`
			}
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got.Apps.TLS.Certificates.Automate, c.want) {
				t.Fatalf("automate = %v, want %v", got.Apps.TLS.Certificates.Automate, c.want)
			}
			for _, p := range got.Apps.TLS.Automation.Policies {
				if p.OnDemand && slices.Contains(p.Subjects, "db.new.com") {
					t.Fatal("db.new.com waits for a visit to get its certificate")
				}
			}
		})
	}
}
