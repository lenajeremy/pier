package pgproxy

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// certificate is a self-signed certificate for db.test.
func certificate(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "db.test"},
		DNSNames:     []string{"db.test", "localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// backend is a stand-in for a Postgres server: it hands over the first
// startup message each connection sends, answers "ready", and echoes what
// comes after.
type backend struct {
	ln       net.Listener
	startups chan []byte
}

func newBackend(t *testing.T) *backend {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	b := &backend{ln: ln, startups: make(chan []byte, 64)}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				var head [4]byte
				if _, err := io.ReadFull(c, head[:]); err != nil {
					return
				}
				pkt := make([]byte, binary.BigEndian.Uint32(head[:]))
				copy(pkt, head[:])
				if _, err := io.ReadFull(c, pkt[4:]); err != nil {
					return
				}
				b.startups <- pkt
				c.Write([]byte("ready"))
				io.Copy(c, c)
			}()
		}
	}()
	return b
}

// proxy runs a Proxy that routes ext_shop to the backend.
func proxy(t *testing.T, b *backend) (string, *[]string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	asked := &[]string{}
	p := &Proxy{
		TLS: &tls.Config{Certificates: []tls.Certificate{certificate(t)}},
		Route: func(_ context.Context, user string) (string, error) {
			mu.Lock()
			*asked = append(*asked, user)
			mu.Unlock()
			if user == "ext_shop" {
				return b.ln.Addr().String(), nil
			}
			return "", errors.New("there's no external URL for " + user)
		},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	done := make(chan error, 1)
	go func() { done <- p.Serve(ln) }()
	t.Cleanup(func() {
		ln.Close()
		if err := <-done; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})
	return ln.Addr().String(), asked
}

// startupMessage is a protocol 3.0 startup message with these parameters.
func startupMessage(params ...string) []byte {
	var body []byte
	body = binary.BigEndian.AppendUint32(body, 196608)
	for _, p := range params {
		body = append(append(body, p...), 0)
	}
	body = append(body, 0)
	return append(binary.BigEndian.AppendUint32(nil, uint32(len(body)+4)), body...)
}

func sslRequestPacket() []byte {
	return binary.BigEndian.AppendUint32(binary.BigEndian.AppendUint32(nil, 8), sslRequest)
}

func dial(t *testing.T, addr string) net.Conn {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	c.SetDeadline(time.Now().Add(10 * time.Second))
	t.Cleanup(func() { c.Close() })
	return c
}

// readError reads an ErrorResponse and returns its fields.
func readError(t *testing.T, r io.Reader) map[byte]string {
	t.Helper()
	var head [5]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		t.Fatalf("no answer: %v", err)
	}
	if head[0] != 'E' {
		t.Fatalf("answered %q, not an error", head[0])
	}
	body := make([]byte, binary.BigEndian.Uint32(head[1:])-4)
	if _, err := io.ReadFull(r, body); err != nil {
		t.Fatal(err)
	}
	fields := map[byte]string{}
	for len(body) > 1 {
		end := bytes.IndexByte(body[1:], 0) + 1
		fields[body[0]] = string(body[1:end])
		body = body[end+1:]
	}
	return fields
}

// closed reports whether the other side closed without a word.
func closed(t *testing.T, c net.Conn) {
	t.Helper()
	var b [1]byte
	n, err := c.Read(b[:])
	if n > 0 || err == nil {
		t.Fatalf("the connection answered %q instead of closing", b[:n])
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		t.Fatal("the connection stayed open")
	}
}

// through checks a connection reaches the backend with the startup
// message unchanged, and carries data both ways.
func through(t *testing.T, c net.Conn, b *backend) {
	t.Helper()
	msg := startupMessage("user", "ext_shop", "database", "production", "application_name", "psql")
	if _, err := c.Write(msg); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-b.startups:
		if !bytes.Equal(got, msg) {
			t.Fatalf("the backend got %q, not %q", got, msg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the backend got nothing")
	}
	ready := make([]byte, 5)
	if _, err := io.ReadFull(c, ready); err != nil || string(ready) != "ready" {
		t.Fatalf("read %q: %v", ready, err)
	}
	c.Write([]byte("ping"))
	echo := make([]byte, 4)
	if _, err := io.ReadFull(c, echo); err != nil || string(echo) != "ping" {
		t.Fatalf("echo %q: %v", echo, err)
	}
}

func TestSSLRequest(t *testing.T) {
	b := newBackend(t)
	addr, asked := proxy(t, b)
	c := dial(t, addr)
	c.Write(sslRequestPacket())
	var answer [1]byte
	if _, err := io.ReadFull(c, answer[:]); err != nil || answer[0] != 'S' {
		t.Fatalf("SSLRequest answered %q: %v", answer, err)
	}
	tc := tls.Client(c, &tls.Config{ServerName: "db.test", InsecureSkipVerify: true})
	through(t, tc, b)
	if len(*asked) != 1 || (*asked)[0] != "ext_shop" {
		t.Fatalf("routed %v", *asked)
	}
}

func TestDirectTLS(t *testing.T) {
	b := newBackend(t)
	addr, _ := proxy(t, b)
	c := dial(t, addr)
	tc := tls.Client(c, &tls.Config{ServerName: "db.test", InsecureSkipVerify: true, NextProtos: []string{ALPN}})
	through(t, tc, b)
	if p := tc.ConnectionState().NegotiatedProtocol; p != ALPN {
		t.Fatalf("negotiated %q", p)
	}

	// Without ALPN, as Postgres does, the connection is refused.
	c = dial(t, addr)
	tc = tls.Client(c, &tls.Config{ServerName: "db.test", InsecureSkipVerify: true})
	if err := tc.Handshake(); err != nil {
		t.Fatal(err)
	}
	tc.Write(startupMessage("user", "ext_shop"))
	closed(t, tc)
	// With another protocol, the handshake fails.
	c = dial(t, addr)
	tc = tls.Client(c, &tls.Config{ServerName: "db.test", InsecureSkipVerify: true, NextProtos: []string{"h2"}})
	if err := tc.Handshake(); err == nil {
		t.Fatal("a handshake for h2 worked")
	}
	if len(b.startups) != 0 {
		t.Fatal("a refused connection reached the backend")
	}
}

func TestPlaintextIsRefused(t *testing.T) {
	b := newBackend(t)
	addr, _ := proxy(t, b)
	c := dial(t, addr)
	c.Write(startupMessage("user", "ext_shop", "database", "production"))
	f := readError(t, c)
	if f['S'] != "FATAL" || f['C'] != "28000" || !strings.Contains(f['M'], "sslmode=require") {
		t.Fatalf("answered %v", f)
	}
	closed(t, c)
	if len(b.startups) != 0 {
		t.Fatal("a plaintext connection reached the backend")
	}
}

func TestUnknownUser(t *testing.T) {
	b := newBackend(t)
	addr, _ := proxy(t, b)
	c := dial(t, addr)
	c.Write(sslRequestPacket())
	io.ReadFull(c, make([]byte, 1))
	tc := tls.Client(c, &tls.Config{ServerName: "db.test", InsecureSkipVerify: true})
	tc.Write(startupMessage("user", "nobody", "database", "production"))
	f := readError(t, tc)
	if f['C'] != "28000" || f['M'] != "there's no external URL for nobody" {
		t.Fatalf("answered %v", f)
	}
	closed(t, tc)

	// A startup message with no user is refused too.
	c = dial(t, addr)
	tc = tls.Client(c, &tls.Config{ServerName: "db.test", InsecureSkipVerify: true, NextProtos: []string{ALPN}})
	tc.Write(startupMessage("database", "production"))
	if f := readError(t, tc); f['C'] != "28000" {
		t.Fatalf("answered %v", f)
	}
}

func TestOversizedPackets(t *testing.T) {
	b := newBackend(t)
	addr, _ := proxy(t, b)
	c := dial(t, addr)
	c.Write(binary.BigEndian.AppendUint32(nil, 1<<20))
	closed(t, c)

	// Over TLS as well.
	c = dial(t, addr)
	tc := tls.Client(c, &tls.Config{ServerName: "db.test", InsecureSkipVerify: true, NextProtos: []string{ALPN}})
	big := startupMessage("user", "ext_shop", "options", strings.Repeat("x", MaxStartup))
	tc.Write(big[:64])
	closed(t, tc)
	if len(b.startups) != 0 {
		t.Fatal("an oversized packet reached the backend")
	}
}

func TestGSSThenSSL(t *testing.T) {
	b := newBackend(t)
	addr, _ := proxy(t, b)
	c := dial(t, addr)
	c.Write(binary.BigEndian.AppendUint32(binary.BigEndian.AppendUint32(nil, 8), gssEncRequest))
	var answer [1]byte
	if _, err := io.ReadFull(c, answer[:]); err != nil || answer[0] != 'N' {
		t.Fatalf("GSSENCRequest answered %q: %v", answer, err)
	}
	c.Write(sslRequestPacket())
	if _, err := io.ReadFull(c, answer[:]); err != nil || answer[0] != 'S' {
		t.Fatalf("SSLRequest answered %q: %v", answer, err)
	}
	through(t, tls.Client(c, &tls.Config{ServerName: "db.test", InsecureSkipVerify: true}), b)
}

func TestCancelIsClosed(t *testing.T) {
	b := newBackend(t)
	addr, _ := proxy(t, b)
	c := dial(t, addr)
	pkt := binary.BigEndian.AppendUint32(binary.BigEndian.AppendUint32(nil, 16), cancelRequest)
	c.Write(binary.BigEndian.AppendUint64(pkt, 42))
	closed(t, c)
}

// A client that never sends anything doesn't hold up the others.
func TestManyAtOnce(t *testing.T) {
	b := newBackend(t)
	addr, _ := proxy(t, b)
	idle := dial(t, addr)
	_ = idle
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := net.Dial("tcp", addr)
			if err != nil {
				errs <- err
				return
			}
			defer c.Close()
			c.SetDeadline(time.Now().Add(10 * time.Second))
			tc := tls.Client(c, &tls.Config{ServerName: "db.test", InsecureSkipVerify: true, NextProtos: []string{ALPN}})
			tc.Write(startupMessage("user", "ext_shop"))
			ready := make([]byte, 5)
			if _, err := io.ReadFull(tc, ready); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestStartupUser(t *testing.T) {
	for _, c := range []struct {
		params []string
		want   string
	}{
		{[]string{"user", "ext_shop", "database", "production"}, "ext_shop"},
		{[]string{"database", "production", "user", "ext_shop"}, "ext_shop"},
		{[]string{"database", "user"}, ""},
		// Postgres takes the last, so the proxy routes by it.
		{[]string{"user", "ext_shop", "user", "postgres"}, "postgres"},
		{nil, ""},
	} {
		if got := startupUser(startupMessage(c.params...)); got != c.want {
			t.Errorf("%v: %q", c.params, got)
		}
	}
}

// TestRealPostgres runs Postgres in Docker behind the proxy and connects
// with pgx, the way an app outside Pier would: PIER_TEST_DOCKER=1.
func TestRealPostgres(t *testing.T) {
	if os.Getenv("PIER_TEST_DOCKER") == "" {
		t.Skip("set PIER_TEST_DOCKER=1 to run Postgres in Docker")
	}
	name := fmt.Sprintf("pier-pgproxy-test-%d", time.Now().UnixNano())
	out, err := exec.Command("docker", "run", "-d", "--rm", "--name", name, "-p", "127.0.0.1::5432",
		"-e", "POSTGRES_PASSWORD=a-long-test-password", "postgres:18-alpine").CombinedOutput()
	if err != nil {
		t.Fatalf("docker run: %v %s", err, out)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", name).Run() })
	out, err = exec.Command("docker", "port", name, "5432/tcp").Output()
	if err != nil {
		t.Fatalf("docker port: %v", err)
	}
	server := strings.TrimSpace(strings.Split(string(out), "\n")[0])
	ctx := context.Background()
	for i := 0; ; i++ {
		conn, err := pgx.Connect(ctx, "postgres://postgres:a-long-test-password@"+server+"/postgres?sslmode=disable&connect_timeout=2")
		if err == nil {
			conn.Close(ctx)
			break
		}
		if i > 120 {
			t.Fatalf("Postgres didn't start: %v", err)
		}
		time.Sleep(500 * time.Millisecond)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &Proxy{
		TLS: &tls.Config{Certificates: []tls.Certificate{certificate(t)}},
		Route: func(_ context.Context, user string) (string, error) {
			if user == "postgres" {
				return server, nil
			}
			return "", errors.New("there's no external URL for " + user)
		},
	}
	go p.Serve(ln)
	t.Cleanup(func() { ln.Close() })
	base := "postgres://postgres:a-long-test-password@" + ln.Addr().String() + "/postgres?connect_timeout=5"

	for _, c := range []struct{ name, params string }{
		{"SSLRequest", "&sslmode=require"},
		{"direct", "&sslmode=require&sslnegotiation=direct"},
	} {
		t.Run(c.name, func(t *testing.T) {
			conn, err := pgx.Connect(ctx, base+c.params)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close(ctx)
			var one int
			var user string
			if err := conn.QueryRow(ctx, "SELECT 1, current_user").Scan(&one, &user); err != nil || one != 1 || user != "postgres" {
				t.Fatalf("SELECT 1: %d %q %v", one, user, err)
			}
			// Many round trips on one connection.
			for i := range 50 {
				var n int
				if err := conn.QueryRow(ctx, "SELECT $1::int + 1", i).Scan(&n); err != nil || n != i+1 {
					t.Fatalf("query %d: %d %v", i, n, err)
				}
			}
		})
	}
	t.Run("wrong password", func(t *testing.T) {
		_, err := pgx.Connect(ctx, strings.Replace(base, "a-long-test-password", "wrong", 1)+"&sslmode=require")
		if err == nil || !strings.Contains(err.Error(), "password authentication failed") {
			t.Fatalf("a wrong password: %v", err)
		}
	})
	t.Run("unknown user", func(t *testing.T) {
		_, err := pgx.Connect(ctx, strings.Replace(base, "//postgres:", "//nobody:", 1)+"&sslmode=require")
		if err == nil || !strings.Contains(err.Error(), "no external URL for nobody") {
			t.Fatalf("an unknown user: %v", err)
		}
	})
	t.Run("plaintext", func(t *testing.T) {
		_, err := pgx.Connect(ctx, base+"&sslmode=disable")
		if err == nil || !strings.Contains(err.Error(), "sslmode=require") {
			t.Fatalf("a plaintext connection: %v", err)
		}
	})
}
