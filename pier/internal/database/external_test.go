package database

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"pier/internal/pgproxy"
)

// tryProxied runs SQL through the TLS port, as a service outside Pier would
// with an external URL, and returns the last result's first value.
func tryProxied(t *testing.T, m *Manager, ext, sql string) (string, error) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "db.example.test"}, DNSNames: []string{"db.example.test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	p := &pgproxy.Proxy{TLS: &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}, Route: m.Route}
	go p.Serve(ln)
	u := strings.Replace(ext, "db.example.test:5432", ln.Addr().String(), 1)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, err := pgconn.Connect(ctx, u)
	if err != nil {
		return "", err
	}
	defer conn.Close(ctx)
	results, err := conn.Exec(ctx, sql).ReadAll()
	if err != nil {
		return "", err
	}
	last := results[len(results)-1]
	if len(last.Rows) == 0 {
		return "", nil
	}
	return string(last.Rows[0][0]), nil
}

func externalThroughProxy(t *testing.T, m *Manager, ext string) func(string) string {
	return func(sql string) string {
		t.Helper()
		out, err := tryProxied(t, m, ext, sql)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
}
