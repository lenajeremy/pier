package alerts

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"pier/internal/secrets"
	"pier/internal/store"
)

// hook is a webhook receiver that keeps what it was sent.
type hook struct {
	mu     sync.Mutex
	bodies [][]byte
	heads  []http.Header
	answer func(n int) int // the status for the nth delivery, from 1
	srv    *httptest.Server
}

func newHook(t *testing.T) *hook {
	h := &hook{answer: func(int) int { return http.StatusOK }}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		h.mu.Lock()
		h.bodies = append(h.bodies, body)
		h.heads = append(h.heads, r.Header.Clone())
		status := h.answer(len(h.bodies))
		h.mu.Unlock()
		w.WriteHeader(status)
		if status >= 300 {
			io.WriteString(w, `{"message":"nope"}`)
		}
	}))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *hook) events(t *testing.T) []map[string]any {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []map[string]any
	for _, b := range h.bodies {
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("not JSON: %s", b)
		}
		out = append(out, m)
	}
	return out
}

func newNotifier(t *testing.T) *Notifier {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "pier.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, err := secrets.Load(filepath.Join(t.TempDir(), "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	return &Notifier{
		Store: st, Box: box, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Dashboard: "https://pier.example.com", Server: "pier.example.com",
		Retries: []time.Duration{}, // no retries unless a test asks for them
	}
}

func (n *Notifier) webhookTo(t *testing.T, h *hook) string {
	t.Helper()
	signing := NewSigningSecret()
	if err := n.Save(context.Background(), Webhook, struct{}{}, map[string]string{secretURL: h.srv.URL, secretSigning: signing}); err != nil {
		t.Fatal(err)
	}
	return signing
}

// raise and resolve handle an event at once, as Run would, and wait for its
// deliveries.
func (n *Notifier) raise(e Event) {
	n.handle(context.Background(), request{e: e})
	n.Wait()
}

func (n *Notifier) resolve(e Event) {
	n.handle(context.Background(), request{e: e, resolve: true})
	n.Wait()
}

func failed(key string, at time.Time) Event {
	return Event{Kind: DeployFailed, Key: key, Project: "blog", Title: "blog: production deploy failed", Summary: "It failed.", At: at}
}

func worked(key string, at time.Time) Event {
	return Event{Kind: DeployRecovered, Key: key, Project: "blog", Title: "blog: production deploys work again", Summary: "It worked.", At: at}
}

// A problem that keeps happening is one alert; its fix is one more, and
// only once; and once fixed, the next failure is a new incident.
func TestOneAlertPerIncidentThenOneRecovery(t *testing.T) {
	n := newNotifier(t)
	h := newHook(t)
	n.webhookTo(t, h)
	t0 := time.Now()

	for i := range 3 {
		n.raise(failed("deploy:blog:production", t0.Add(time.Duration(i)*time.Minute)))
	}
	if got := h.events(t); len(got) != 1 || got[0]["event"] != DeployFailed {
		t.Fatalf("three failures sent %v, want one alert", got)
	}
	n.raise(failed("deploy:other:production", t0))
	if got := h.events(t); len(got) != 2 {
		t.Fatalf("another project's failure wasn't its own incident: %d sent", len(got))
	}

	n.resolve(worked("deploy:blog:production", t0.Add(5*time.Minute)))
	got := h.events(t)
	if len(got) != 3 || got[2]["event"] != DeployRecovered || got[2]["recovered"] != true {
		t.Fatalf("the fix sent %v, want a recovery", got)
	}
	if s, _ := got[2]["summary"].(string); !strings.Contains(s, "first alert about it was sent 5 minutes ago") {
		t.Fatalf("recovery summary = %q", s)
	}
	n.resolve(worked("deploy:blog:production", t0.Add(6*time.Minute)))
	if got := h.events(t); len(got) != 3 {
		t.Fatalf("a second fix sent another recovery: %d sent", len(got))
	}
	n.resolve(worked("deploy:never:production", t0))
	if got := h.events(t); len(got) != 3 {
		t.Fatal("a fix with no alert before it sent a recovery")
	}

	n.raise(failed("deploy:blog:production", t0.Add(10*time.Minute)))
	if got := h.events(t); len(got) != 4 {
		t.Fatalf("a failure after the fix sent %d, want a new alert", len(got)-3)
	}
}

// An incident that goes quiet for longer than Quiet, like a branch that
// failed and was left, alerts again the next time.
func TestQuietIncidentAlertsAgain(t *testing.T) {
	n := newNotifier(t)
	n.Quiet = time.Hour
	h := newHook(t)
	n.webhookTo(t, h)
	t0 := time.Now()
	n.raise(failed("deploy:blog:preview:feat", t0))
	n.raise(failed("deploy:blog:preview:feat", t0.Add(50*time.Minute)))
	n.raise(failed("deploy:blog:preview:feat", t0.Add(100*time.Minute)))
	if got := h.events(t); len(got) != 1 {
		t.Fatalf("failures within the quiet time sent %d alerts", len(got))
	}
	n.raise(failed("deploy:blog:preview:feat", t0.Add(4*time.Hour)))
	if got := h.events(t); len(got) != 2 {
		t.Fatalf("a failure after a quiet spell sent %d alerts, want 2 in all", len(got))
	}
}

// Many problems at once never flood: past the limit they're held back and
// counted, and the next alert that goes out says so.
func TestRateLimitHoldsBackAndSaysSo(t *testing.T) {
	n := newNotifier(t)
	n.Limit, n.Window = 2, time.Hour
	h := newHook(t)
	n.webhookTo(t, h)
	t0 := time.Now()
	for i, p := range []string{"a", "b", "c", "d"} {
		n.raise(failed("deploy:"+p+":production", t0.Add(time.Duration(i)*time.Second)))
	}
	if got := h.events(t); len(got) != 2 {
		t.Fatalf("four problems in a window of two sent %d", len(got))
	}
	n.raise(failed("deploy:e:production", t0.Add(2*time.Hour)))
	got := h.events(t)
	if len(got) != 3 {
		t.Fatalf("after the window, %d sent in all, want 3", len(got))
	}
	if s, _ := got[2]["summary"].(string); !strings.Contains(s, "held back 2 other alerts in the last 1 hour") {
		t.Fatalf("summary after holding back = %q", s)
	}
}

// Failed previews can be silenced; production's can't.
func TestPreviewAlertsCanBeTurnedOff(t *testing.T) {
	n := newNotifier(t)
	h := newHook(t)
	n.webhookTo(t, h)
	if err := n.Store.SetSetting(context.Background(), store.SettingPreviewAlerts, "off"); err != nil {
		t.Fatal(err)
	}
	e := failed("deploy:blog:preview:feat", time.Now())
	e.Preview = true
	n.raise(e)
	n.raise(failed("deploy:blog:production", time.Now()))
	if got := h.events(t); len(got) != 1 || got[0]["title"] != "blog: production deploy failed" {
		t.Fatalf("sent %v, want production's alone", got)
	}
}

// Every webhook delivery is signed over its exact body with the secret
// Pier made, and says which event it is.
func TestWebhookSignature(t *testing.T) {
	n := newNotifier(t)
	h := newHook(t)
	signing := n.webhookTo(t, h)
	e := failed("deploy:blog:production", time.Now())
	e.Lines = []string{"build   npm ERR! missing script: build"}
	e.Link = "/projects/blog/deployments/c19e77"
	n.raise(e)
	h.mu.Lock()
	body, head := h.bodies[0], h.heads[0]
	h.mu.Unlock()
	sig := head.Get(SignatureHeader)
	if !strings.HasPrefix(sig, "sha256=") || !Verify(signing, body, sig) {
		t.Fatalf("signature %q doesn't verify over the body", sig)
	}
	if Verify(signing, append(body, ' '), sig) || Verify("whsec_other", body, sig) {
		t.Fatal("a changed body or another secret verified")
	}
	if head.Get("X-Pier-Event") != DeployFailed || head.Get("X-Pier-Delivery") == "" || head.Get("Content-Type") != "application/json" {
		t.Fatalf("headers = %v", head)
	}
	var got map[string]any
	json.Unmarshal(body, &got)
	if got["url"] != "https://pier.example.com/projects/blog/deployments/c19e77" || got["server"] != "pier.example.com" || got["project"] != "blog" {
		t.Fatalf("body = %s", body)
	}
	if lines, _ := got["lines"].([]any); len(lines) != 1 {
		t.Fatalf("lines = %v", got["lines"])
	}
}

// A delivery that fails for a while is tried again; one the other end
// refuses outright isn't.
func TestDeliveryRetries(t *testing.T) {
	n := newNotifier(t)
	n.Retries = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	h := newHook(t)
	h.answer = func(i int) int {
		if i < 3 {
			return http.StatusBadGateway
		}
		return http.StatusOK
	}
	n.webhookTo(t, h)
	n.raise(failed("deploy:blog:production", time.Now()))
	if got := h.events(t); len(got) != 3 {
		t.Fatalf("delivered after %d tries, want 3", len(got))
	}
	chans, _ := n.Store.AlertChannels(context.Background())
	if c := chans[Webhook]; c.LastSent.IsZero() || c.LastError != "" {
		t.Fatalf("after a delivery that worked: %+v", c)
	}

	h2 := newHook(t)
	h2.answer = func(int) int { return http.StatusNotFound }
	n.webhookTo(t, h2)
	n.raise(failed("deploy:other:production", time.Now()))
	if got := h2.events(t); len(got) != 1 {
		t.Fatalf("a 404 was tried %d times, want once", len(got))
	}
	chans, _ = n.Store.AlertChannels(context.Background())
	if c := chans[Webhook]; c.LastError != "The webhook answered 404 Not Found: nope" {
		t.Fatalf("last error = %q", c.LastError)
	}
}

// Slack and Discord are told apart by their URLs, and each gets its own
// format.
func TestSlackAndDiscord(t *testing.T) {
	for raw, want := range map[string]string{
		"https://hooks.slack.com/services/T000/B000/XXXX":              Slack,
		"https://discord.com/api/webhooks/123/abc":                     Discord,
		"https://discordapp.com/api/webhooks/123/abc":                  Discord,
		"https://canary.discord.com/api/webhooks/123/abc":              Discord,
		"http://hooks.slack.com/services/T000/B000/XXXX":               "",
		"https://hooks.slack.com/workflows/T000/A000/1/abc":            "",
		"https://example.com/api/webhooks/123/abc":                     "",
		"https://discord.com.evil.example/api/webhooks/123/abc":        "",
		"https://hooks.slack.com.evil.example/services/T000/B000/XXXX": "",
	} {
		got, err := ChatService(raw)
		if got != want || (want == "") != (err != nil) {
			t.Errorf("ChatService(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}

	e := Event{
		Kind: DeployFailed, Title: "blog: production deploy failed", Summary: "It broke <here> & there.",
		Project: "blog", Branch: "main", Commit: "a1b2c3d4e5f6", Message: "Fix @everyone",
		Lines: []string{"build   npm ERR! ```oops```"}, Link: "https://pier.example.com/projects/blog", At: time.Now(),
	}
	slack, _ := json.Marshal(slackMessage(e))
	for _, want := range []string{`"text":"blog: production deploy failed"`, `"type":"mrkdwn"`, `It broke \u0026lt;here\u0026gt; \u0026amp; there.`, `"type":"button"`, `"url":"https://pier.example.com/projects/blog"`, "a1b2c3d Fix @everyone"} {
		if !strings.Contains(string(slack), want) {
			t.Errorf("Slack message lacks %s:\n%s", want, slack)
		}
	}
	if strings.Contains(string(slack), "embeds") || strings.Contains(string(slack), "```oops```") {
		t.Errorf("Slack message has Discord's parts, or an unbroken code fence:\n%s", slack)
	}
	discord, _ := json.Marshal(discordMessage(e))
	for _, want := range []string{`"embeds":[`, `"color":16744576`, `"allowed_mentions":{"parse":[]}`, `"url":"https://pier.example.com/projects/blog"`, `"name":"Branch","value":"main"`, `"username":"Pier"`} {
		if !strings.Contains(string(discord), want) {
			t.Errorf("Discord message lacks %s:\n%s", want, discord)
		}
	}
	if strings.Contains(string(discord), "blocks") {
		t.Errorf("Discord message has Slack's blocks:\n%s", discord)
	}
	fixed, _ := json.Marshal(discordMessage(Event{Kind: DeployRecovered, Title: "fixed", At: time.Now()}))
	if !strings.Contains(string(fixed), `"color":6478022`) {
		t.Errorf("a recovery isn't teal: %s", fixed)
	}

	// Each is posted to its own URL, whose host stays Slack's or Discord's.
	for _, c := range []struct{ url, want string }{
		{"https://hooks.slack.com/services/T000/B000/XXXX", `"blocks"`},
		{"https://discord.com/api/webhooks/123/abc", `"embeds"`},
	} {
		n := newNotifier(t)
		h := newHook(t)
		var host, path string
		n.Client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			host, path = r.URL.Host, r.URL.Path
			to, _ := url.Parse(h.srv.URL)
			r = r.Clone(r.Context())
			r.URL.Scheme, r.URL.Host = to.Scheme, to.Host
			return http.DefaultTransport.RoundTrip(r)
		})}
		service, _ := ChatService(c.url)
		if err := n.Save(context.Background(), Chat, ChatSettings{Service: service}, map[string]string{secretURL: c.url}); err != nil {
			t.Fatal(err)
		}
		if err := n.Test(context.Background(), Chat); err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse(c.url)
		h.mu.Lock()
		body := string(h.bodies[0])
		h.mu.Unlock()
		if host != u.Host || path != u.Path || !strings.Contains(body, c.want) {
			t.Errorf("posted to %s%s: %s", host, path, body)
		}
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// smtpServer is a mail server on loopback that takes PLAIN auth and keeps
// each message it's given.
type smtpServer struct {
	addr  string
	mu    sync.Mutex
	auth  []string // the user and password each session signed in with
	mails []struct{ from, to, data string }
}

func newSMTP(t *testing.T) *smtpServer {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	s := &smtpServer{addr: ln.Addr().String()}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(conn)
		}
	}()
	return s
}

func (s *smtpServer) serve(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	say := func(line string) { io.WriteString(conn, line+"\r\n") }
	say("220 fake ESMTP")
	var from, to string
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		verb := strings.ToUpper(strings.Fields(line + " x")[0])
		switch verb {
		case "EHLO", "HELO":
			say("250-fake")
			say("250 AUTH PLAIN")
		case "AUTH":
			raw, _ := base64.StdEncoding.DecodeString(strings.Fields(line)[2])
			parts := strings.Split(string(raw), "\x00")
			s.mu.Lock()
			s.auth = append(s.auth, parts[1]+":"+parts[2])
			s.mu.Unlock()
			if parts[2] != "the password" {
				say("535 5.7.8 Authentication credentials invalid")
				continue
			}
			say("235 2.7.0 Authentication successful")
		case "MAIL":
			from = line
			say("250 OK")
		case "RCPT":
			to = line
			say("250 OK")
		case "DATA":
			say("354 go on")
			var data strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				data.WriteString(l)
			}
			s.mu.Lock()
			s.mails = append(s.mails, struct{ from, to, data string }{from, to, data.String()})
			s.mu.Unlock()
			say("250 OK queued")
		case "RSET", "NOOP":
			say("250 OK")
		case "QUIT":
			say("221 bye")
			return
		default:
			say("502 unknown")
		}
	}
}

// Alert emails go out over SMTP, one message to each person who has them
// on, and a wrong password says what the server said.
func TestEmailOverSMTP(t *testing.T) {
	ctx := context.Background()
	n := newNotifier(t)
	mail := newSMTP(t)
	host, port, _ := net.SplitHostPort(mail.addr)
	var p int
	json.Unmarshal([]byte(port), &p)
	for _, who := range []string{"owner@example.com", "member@example.com", "quiet@example.com"} {
		if _, err := n.Store.CreateUser(ctx, who, "", "x"); err != nil {
			t.Fatal(err)
		}
	}
	quiet, _ := n.Store.UserByEmail(ctx, "quiet@example.com")
	n.Store.SetAlertEmails(ctx, quiet.ID, false)
	settings := EmailSettings{Provider: SMTP, Host: host, Port: p, User: "pier", From: "Pier <alerts@example.com>"}
	if err := n.Save(ctx, Email, settings, map[string]string{secretPassword: "the password"}); err != nil {
		t.Fatal(err)
	}
	e := failed("deploy:blog:production", time.Now())
	e.Lines = []string{"build   npm ERR! missing script: build"}
	e.Link = "/projects/blog/deployments/c19e77"
	n.raise(e)
	mail.mu.Lock()
	mails := mail.mails
	mail.mu.Unlock()
	if len(mails) != 2 || !strings.Contains(mails[0].to, "owner@example.com") || !strings.Contains(mails[1].to, "member@example.com") {
		t.Fatalf("mails = %+v", mails)
	}
	data := mails[0].data
	for _, want := range []string{"Subject: blog: production deploy failed", "To: owner@example.com", "From: Pier <alerts@example.com>", "npm ERR! missing script: build", "https://pier.example.com/projects/blog/deployments/c19e77"} {
		if !strings.Contains(strings.ReplaceAll(data, "=\r\n", ""), want) {
			t.Errorf("mail lacks %q:\n%s", want, data)
		}
	}
	if strings.Contains(data, "member@example.com") {
		t.Error("one person's mail names another")
	}

	if err := n.Save(ctx, Email, settings, map[string]string{secretPassword: "wrong"}); err != nil {
		t.Fatal(err)
	}
	err := n.Test(ctx, Email)
	if err == nil || err.Error() != "the mail server answered 535 5.7.8 Authentication credentials invalid" {
		t.Fatalf("a wrong password reported %v", err)
	}
}

// Through Resend, each person gets their own message, sent with the key.
func TestEmailThroughResend(t *testing.T) {
	ctx := context.Background()
	n := newNotifier(t)
	var mu sync.Mutex
	var got []map[string]any
	var keys []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		json.NewDecoder(r.Body).Decode(&m)
		mu.Lock()
		got = append(got, m)
		keys = append(keys, r.Header.Get("Authorization"))
		mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer re_good" {
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, `{"statusCode":403,"message":"API key is invalid","name":"validation_error"}`)
			return
		}
		io.WriteString(w, `{"id":"1"}`)
	}))
	t.Cleanup(api.Close)
	n.ResendURL = api.URL
	n.Store.CreateUser(ctx, "owner@example.com", "", "x")
	n.Save(ctx, Email, EmailSettings{Provider: Resend, From: "alerts@example.com"}, map[string]string{secretAPIKey: "re_good"})
	if err := n.Test(ctx, Email); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0]["from"] != "alerts@example.com" || got[0]["subject"] != "Test alert from Pier" || keys[0] != "Bearer re_good" {
		t.Fatalf("Resend got %v with %v", got, keys)
	}
	if to, _ := got[0]["to"].([]any); len(to) != 1 || to[0] != "owner@example.com" {
		t.Fatalf("to = %v", got[0]["to"])
	}
	n.Save(ctx, Email, EmailSettings{Provider: Resend, From: "alerts@example.com"}, map[string]string{secretAPIKey: "re_bad"})
	if err := n.Test(ctx, Email); err == nil || err.Error() != "Resend answered 403 Forbidden: API key is invalid" {
		t.Fatalf("a bad key reported %v", err)
	}
}
