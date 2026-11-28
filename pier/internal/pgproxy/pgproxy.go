// Package pgproxy is the public front door to projects' Postgres servers,
// for apps outside Pier that use an external database URL. pierd listens
// on port 5432 and hands each connection to a Proxy, which ends the TLS
// connection with Pier's certificate, reads the role the client asks for,
// and passes the connection on, unchanged, to the server that role belongs
// to, over loopback. Postgres checks the password itself, through the
// proxy, so the proxy never sees one. Nothing is passed on without TLS.
package pgproxy

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"slices"
	"strings"
	"time"
)

// ALPN is the protocol name Postgres clients ask for over TLS.
const ALPN = "postgresql"

// Codes in the first packet a client sends, in place of a protocol version.
const (
	sslRequest    = 80877103
	gssEncRequest = 80877104
	cancelRequest = 80877102
)

// MaxStartup is the largest first packet or startup message the proxy
// reads, as Postgres allows.
const MaxStartup = 10000

// StartupTimeout is how long a client has to finish TLS and send its
// startup message. Once it's passed on, the connection has no time limit.
const StartupTimeout = 10 * time.Second

// SQLSTATE codes the proxy answers with.
const (
	codeAuthorization = "28000" // invalid_authorization_specification
	codeConnect       = "08001" // sqlclient_unable_to_establish_sqlconnection
	codeProtocol      = "08P01" // protocol_violation
)

// Proxy passes Postgres connections from the internet on to projects'
// servers, by the role each client connects as.
type Proxy struct {
	// TLS holds the server's certificate. ALPN is added to its NextProtos.
	TLS *tls.Config
	// Route is the address ("host:port") of the server a role belongs to,
	// or an error, whose words the client sees.
	Route func(ctx context.Context, user string) (string, error)
	// Log hears about connections refused and why. Nil logs nothing.
	Log *slog.Logger
	// Dial reaches a server; nil dials TCP. For tests.
	Dial func(ctx context.Context, address string) (net.Conn, error)
}

// refusal is a connection the proxy turns away, with what the client is
// told and what the log says.
type refusal struct {
	code   string // SQLSTATE for the client; empty closes without a word
	tell   string // what the client is told
	reason string // what the log says
}

func (r *refusal) Error() string { return r.reason }

func refuse(code, tell, reason string) *refusal {
	return &refusal{code: code, tell: tell, reason: reason}
}

// Serve accepts connections on ln and handles each on its own. It returns
// nil once ln is closed.
func (p *Proxy) Serve(ln net.Listener) error {
	if p.TLS == nil || p.Route == nil {
		return errors.New("pgproxy: a Proxy needs TLS and Route")
	}
	cfg := p.TLS.Clone()
	if !slices.Contains(cfg.NextProtos, ALPN) {
		cfg.NextProtos = append(cfg.NextProtos, ALPN)
	}
	if cfg.MinVersion < tls.VersionTLS12 {
		cfg.MinVersion = tls.VersionTLS12
	}
	var pause time.Duration
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			// Running out of files or a connection reset before it was
			// accepted passes; wait a little, as net/http does.
			var t interface{ Temporary() bool }
			if errors.As(err, &t) && t.Temporary() {
				pause = min(max(2*pause, 5*time.Millisecond), time.Second)
				p.log().Warn("postgres proxy: accept", "err", err, "retry_in", pause)
				time.Sleep(pause)
				continue
			}
			return err
		}
		pause = 0
		go p.handle(c, cfg)
	}
}

func (p *Proxy) log() *slog.Logger {
	if p.Log != nil {
		return p.Log
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// handle takes one connection from its first byte to its last.
func (p *Proxy) handle(c net.Conn, cfg *tls.Config) {
	remote := c.RemoteAddr().String()
	c.SetDeadline(time.Now().Add(StartupTimeout))
	client, startup, user, err := p.startup(c, cfg)
	if err != nil {
		p.close(client, c, remote, user, err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), StartupTimeout)
	defer cancel()
	address, err := p.Route(ctx, user)
	if err != nil {
		p.close(client, c, remote, user, refuse(codeAuthorization, err.Error(), "no route: "+err.Error()))
		return
	}
	server, err := p.dial(ctx, address)
	if err != nil {
		p.close(client, c, remote, user, refuse(codeConnect, "The database isn't answering. Try again in a moment.", "dial "+address+": "+err.Error()))
		return
	}
	if _, err := server.Write(startup); err != nil {
		server.Close()
		p.close(client, c, remote, user, refuse(codeConnect, "The database isn't answering. Try again in a moment.", "write the startup message: "+err.Error()))
		return
	}
	c.SetDeadline(time.Time{})
	p.log().Debug("postgres proxy: connected", "remote", remote, "user", user, "server", address)
	splice(client, server)
}

func (p *Proxy) dial(ctx context.Context, address string) (net.Conn, error) {
	if p.Dial != nil {
		return p.Dial(ctx, address)
	}
	var d net.Dialer
	return d.DialContext(ctx, "tcp", address)
}

// close turns a connection away: with an ErrorResponse when the client
// can read one, and a line in the log either way.
func (p *Proxy) close(client, raw net.Conn, remote, user string, err error) {
	var r *refusal
	if !errors.As(err, &r) {
		r = refuse("", "", err.Error())
	}
	if r.code != "" && client != nil {
		client.Write(errorResponse(r.code, r.tell))
	}
	args := []any{"remote", remote, "reason", r.reason}
	if user != "" {
		args = append(args, "user", user)
	}
	p.log().Info("postgres proxy: refused a connection", args...)
	if client != nil {
		client.Close()
	}
	raw.Close()
}

// startup negotiates TLS and reads the startup message. client is the
// connection to answer on: the TLS one once there is one, the raw one
// before. It's nil when the client can't be told anything.
func (p *Proxy) startup(c net.Conn, cfg *tls.Config) (client net.Conn, startup []byte, user string, err error) {
	client = c
	for round := 0; ; round++ {
		if round > 1 {
			return nil, nil, "", refuse("", "", "asked for encryption more than twice")
		}
		var first [1]byte
		if _, err := io.ReadFull(c, first[:]); err != nil {
			return nil, nil, "", refuse("", "", "read the first packet: "+err.Error())
		}
		if first[0] == 0x16 {
			// A TLS handshake straight away: Postgres 17's direct SSL,
			// which must name the protocol with ALPN.
			t := tls.Server(&prefixed{Conn: c, prefix: first[:]}, cfg)
			if err := t.Handshake(); err != nil {
				return nil, nil, "", refuse("", "", "direct TLS handshake: "+err.Error())
			}
			if t.ConnectionState().NegotiatedProtocol != ALPN {
				t.Close()
				return nil, nil, "", refuse("", "", "direct TLS without ALPN "+ALPN)
			}
			return p.readStartup(t)
		}
		pkt, err := readPacket(c, first[:])
		if err != nil {
			return nil, nil, "", err
		}
		switch code := binary.BigEndian.Uint32(pkt[4:8]); {
		case code == sslRequest && len(pkt) == 8:
			if _, err := c.Write([]byte{'S'}); err != nil {
				return nil, nil, "", refuse("", "", "answer SSLRequest: "+err.Error())
			}
			t := tls.Server(c, cfg)
			if err := t.Handshake(); err != nil {
				return nil, nil, "", refuse("", "", "TLS handshake: "+err.Error())
			}
			return p.readStartup(t)
		case code == gssEncRequest && len(pkt) == 8:
			if _, err := c.Write([]byte{'N'}); err != nil {
				return nil, nil, "", refuse("", "", "answer GSSENCRequest: "+err.Error())
			}
		case code == cancelRequest:
			// A cancel names a backend by its key, not a role, so the
			// proxy can't tell which server it's for.
			return nil, nil, "", refuse("", "", "a cancel request")
		case code>>16 == 3:
			return client, nil, startupUser(pkt), refuse(codeAuthorization, "Connect with sslmode=require: Pier's databases only take encrypted connections from outside.", "no TLS")
		default:
			return nil, nil, "", refuse("", "", fmt.Sprintf("an unknown first packet (code %d)", code))
		}
	}
}

// readStartup reads the startup message over TLS and the role it names.
func (p *Proxy) readStartup(t *tls.Conn) (net.Conn, []byte, string, error) {
	var first [1]byte
	if _, err := io.ReadFull(t, first[:]); err != nil {
		return t, nil, "", refuse("", "", "read the startup message: "+err.Error())
	}
	pkt, err := readPacket(t, first[:])
	if err != nil {
		return t, nil, "", err
	}
	version := binary.BigEndian.Uint32(pkt[4:8])
	if version>>16 != 3 {
		if version == cancelRequest {
			return t, nil, "", refuse("", "", "a cancel request over TLS")
		}
		return t, nil, "", refuse(codeProtocol, "Pier's databases speak protocol 3.", fmt.Sprintf("protocol version %d after TLS", version))
	}
	user := startupUser(pkt)
	if user == "" {
		return t, nil, "", refuse(codeAuthorization, "Name a user to connect as.", "no user in the startup message")
	}
	return t, pkt, user, nil
}

// readPacket reads a packet whose first byte is read already: a length,
// counting itself, and the rest.
func readPacket(r io.Reader, first []byte) ([]byte, error) {
	var head [4]byte
	head[0] = first[0]
	if _, err := io.ReadFull(r, head[1:]); err != nil {
		return nil, refuse("", "", "read a packet's length: "+err.Error())
	}
	n := binary.BigEndian.Uint32(head[:])
	if n < 8 || n > MaxStartup {
		return nil, refuse("", "", fmt.Sprintf("a first packet of %d bytes", n))
	}
	pkt := make([]byte, n)
	copy(pkt, head[:])
	if _, err := io.ReadFull(r, pkt[4:]); err != nil {
		return nil, refuse("", "", "read a packet: "+err.Error())
	}
	return pkt, nil
}

// startupUser is the user a startup message names: its parameters follow
// the version as pairs of null-terminated strings. Postgres takes the last
// "user" when one is named twice, so the proxy does too: routing by the
// first would let a client reach a server as a role of the second.
func startupUser(pkt []byte) string {
	fields := strings.Split(string(pkt[8:]), "\x00")
	user := ""
	for i := 0; i+1 < len(fields); i += 2 {
		if fields[i] == "" {
			break
		}
		if fields[i] == "user" {
			user = fields[i+1]
		}
	}
	return user
}

// errorResponse is a FATAL ErrorResponse message.
func errorResponse(code, message string) []byte {
	var body []byte
	field := func(kind byte, s string) {
		body = append(body, kind)
		body = append(body, strings.ReplaceAll(s, "\x00", "")...)
		body = append(body, 0)
	}
	field('S', "FATAL")
	field('V', "FATAL")
	field('C', code)
	field('M', message)
	body = append(body, 0)
	msg := []byte{'E', 0, 0, 0, 0}
	binary.BigEndian.PutUint32(msg[1:], uint32(len(body)+4))
	return append(msg, body...)
}

// prefixed is a connection with bytes already read put back in front.
type prefixed struct {
	net.Conn
	prefix []byte
}

func (c *prefixed) Read(b []byte) (int, error) {
	if len(c.prefix) > 0 {
		n := copy(b, c.prefix)
		c.prefix = c.prefix[n:]
		return n, nil
	}
	return c.Conn.Read(b)
}

// Grace is how long the other direction may go on after one side of a
// connection finishes, before both are closed.
const Grace = 5 * time.Second

// splice copies both ways until one side finishes, gives the other a
// moment to finish too, and closes both.
func splice(client, server net.Conn) {
	done := make(chan struct{}, 2)
	copyTo := func(dst, src net.Conn) {
		io.Copy(dst, src)
		if w, ok := dst.(interface{ CloseWrite() error }); ok {
			w.CloseWrite()
		}
		done <- struct{}{}
	}
	go copyTo(server, client)
	go copyTo(client, server)
	<-done
	select {
	case <-done:
	case <-time.After(Grace):
	}
	client.Close()
	server.Close()
}
