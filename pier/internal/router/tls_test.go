package router

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
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
