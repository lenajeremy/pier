package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"pier/internal/alerts"
	"pier/internal/auth"
	"pier/internal/deploy"
	"pier/internal/secrets"
	"pier/internal/store"
)

// alertServer is an API with alerts, and a browser signed in to it.
func alertServer(t *testing.T) (*httptest.Server, *http.Client, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "pier.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hash, _ := auth.HashPassword("correct horse battery")
	st.CreateUser(context.Background(), "owner@example.com", "Owner", hash)
	box, _ := secrets.Load(filepath.Join(t.TempDir(), "secret.key"))
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := &API{
		Store: st, Log: log,
		Deployer: &deploy.Deployer{Store: st, Domain: "pier.localhost", PierHost: "pier.localhost", ProductionBranch: "main", Secrets: box, URL: func(h string) string { return "http://" + h }},
		Alerts:   &alerts.Notifier{Store: st, Box: box, Log: log, Dashboard: "http://pier.localhost", Server: "pier.localhost", Retries: []time.Duration{}},
	}
	srv := httptest.NewServer(a.Public())
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	if resp, _ := do(t, c, "POST", srv.URL+"/api/session", `{"email":"owner@example.com","password":"correct horse battery"}`, true); resp.StatusCode != 200 {
		t.Fatalf("sign in: %d", resp.StatusCode)
	}
	return srv, c, st
}

func raw(t *testing.T, c *http.Client, method, url, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("X-Pier", "1")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// Passwords, keys, webhook URLs and signing secrets go in and never come
// out: the browser learns only that each is set. The signing secret is
// shown once, when Pier makes it.
func TestAlertSecretsNeverReturned(t *testing.T) {
	srv, c, _ := alertServer(t)
	var mu sync.Mutex
	var signature string
	var body []byte
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		body, _ = io.ReadAll(r.Body)
		signature = r.Header.Get(alerts.SignatureHeader)
	}))
	t.Cleanup(hook.Close)
	hookURL := hook.URL + "/hooks/s3cr3t-path"
	slackURL := "https://hooks.slack.com/services/T000/B000/SLACKSECRET"

	if code, out := raw(t, c, "PUT", srv.URL+"/api/alerts/email", `{"provider":"smtp","host":"smtp.example.com","port":587,"user":"pier","password":"hunter2-smtp","from":"alerts@example.com"}`); code != 200 {
		t.Fatalf("save email: %d %s", code, out)
	}
	if code, out := raw(t, c, "PUT", srv.URL+"/api/alerts/chat", `{"url":"`+slackURL+`"}`); code != 200 || !strings.Contains(out, `"service":"slack"`) {
		t.Fatalf("save chat: %d %s", code, out)
	}
	code, out := raw(t, c, "PUT", srv.URL+"/api/alerts/webhook", `{"url":"`+hookURL+`"}`)
	var first struct {
		Signing string `json:"signing_secret"`
	}
	json.Unmarshal([]byte(out), &first)
	if code != 200 || !strings.HasPrefix(first.Signing, "whsec_") {
		t.Fatalf("save webhook: %d %s", code, out)
	}
	if strings.Contains(out, "s3cr3t-path") {
		t.Fatalf("saving the webhook answered with its URL: %s", out)
	}

	// Saving again keeps every secret and shows none.
	if code, out := raw(t, c, "PUT", srv.URL+"/api/alerts/webhook", `{}`); code != 200 || strings.Contains(out, "signing_secret") {
		t.Fatalf("saving the webhook again: %d %s", code, out)
	}
	if code, out := raw(t, c, "PUT", srv.URL+"/api/alerts/email", `{"provider":"smtp","host":"smtp.example.com","port":2525,"user":"pier","from":"alerts@example.com"}`); code != 200 || !strings.Contains(out, `"set":["password"]`) {
		t.Fatalf("changing email without the password: %d %s", code, out)
	}

	code, out = raw(t, c, "GET", srv.URL+"/api/alerts", "")
	if code != 200 {
		t.Fatalf("alerts: %d %s", code, out)
	}
	for _, secret := range []string{"hunter2-smtp", "SLACKSECRET", "hooks.slack.com", "s3cr3t-path", hook.URL, first.Signing, first.Signing[6:20]} {
		if strings.Contains(out, secret) {
			t.Fatalf("GET /api/alerts gave away %q: %s", secret, out)
		}
	}
	for _, want := range []string{`"port":2525`, `"user":"pier"`, `"service":"slack"`, `"set":["url","signing"]`, `"recipients":[{"name":"Owner","email":"owner@example.com"}]`, `"previews":true`} {
		if !strings.Contains(out, want) {
			t.Fatalf("GET /api/alerts lacks %s: %s", want, out)
		}
	}

	// The test alert arrives signed with the secret that was shown.
	if code, out := raw(t, c, "POST", srv.URL+"/api/alerts/webhook/test", ""); code != 200 {
		t.Fatalf("test webhook: %d %s", code, out)
	}
	mu.Lock()
	ok := alerts.Verify(first.Signing, body, signature)
	mu.Unlock()
	if !ok {
		t.Fatal("the test alert isn't signed with the secret Pier showed")
	}

	// A new secret is shown once, and signs from then on.
	code, out = raw(t, c, "POST", srv.URL+"/api/alerts/webhook/secret", "")
	var next struct {
		Signing string `json:"signing_secret"`
	}
	json.Unmarshal([]byte(out), &next)
	if code != 200 || next.Signing == "" || next.Signing == first.Signing {
		t.Fatalf("new secret: %d %s", code, out)
	}
	raw(t, c, "POST", srv.URL+"/api/alerts/webhook/test", "")
	mu.Lock()
	ok = alerts.Verify(next.Signing, body, signature)
	mu.Unlock()
	if !ok {
		t.Fatal("the new secret doesn't sign the next alert")
	}

	if code, out := raw(t, c, "PUT", srv.URL+"/api/alerts/chat", `{"url":"https://example.com/hook"}`); code != 400 || !strings.Contains(out, `"field":"url"`) {
		t.Fatalf("a URL that's neither Slack's nor Discord's: %d %s", code, out)
	}
	if code, _ := raw(t, c, "DELETE", srv.URL+"/api/alerts/chat", ""); code != 204 {
		t.Fatalf("remove chat: %d", code)
	}
	if _, out := raw(t, c, "GET", srv.URL+"/api/alerts", ""); strings.Contains(out, `"chat"`) {
		t.Fatalf("chat still set up: %s", out)
	}
}

// Send a test alert answers with exactly what the other end said.
func TestTestAlertReportsTheError(t *testing.T) {
	srv, c, _ := alertServer(t)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, "this receiver wants a token")
	}))
	t.Cleanup(hook.Close)
	if code, out := raw(t, c, "POST", srv.URL+"/api/alerts/webhook/test", ""); code != 409 {
		t.Fatalf("testing a channel that isn't set up: %d %s", code, out)
	}
	raw(t, c, "PUT", srv.URL+"/api/alerts/webhook", `{"url":"`+hook.URL+`"}`)
	code, out := raw(t, c, "POST", srv.URL+"/api/alerts/webhook/test", "")
	if code != 502 || !strings.Contains(out, "The webhook answered 403 Forbidden: this receiver wants a token") {
		t.Fatalf("test: %d %s", code, out)
	}
	if _, out := raw(t, c, "GET", srv.URL+"/api/alerts", ""); !strings.Contains(out, `"last_error":"The webhook answered 403 Forbidden: this receiver wants a token"`) {
		t.Fatalf("the failure isn't kept: %s", out)
	}
}

// Each person turns their own alert emails off and on.
func TestAlertEmailsPerPerson(t *testing.T) {
	srv, c, _ := alertServer(t)
	if code, out := raw(t, c, "GET", srv.URL+"/api/account/alerts", ""); code != 200 || out != "{\"email\":true}\n" {
		t.Fatalf("by default: %d %s", code, out)
	}
	if code, out := raw(t, c, "PUT", srv.URL+"/api/account/alerts", `{"email":false}`); code != 200 || out != "{\"email\":false}\n" {
		t.Fatalf("off: %d %s", code, out)
	}
	if _, out := raw(t, c, "GET", srv.URL+"/api/alerts", ""); !strings.Contains(out, `"recipients":[]`) {
		t.Fatalf("still a recipient: %s", out)
	}
	req, _ := http.NewRequest("GET", srv.URL+"/api/account/alerts", nil)
	req.Header.Set("Authorization", "Bearer nope")
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != 401 {
		t.Fatalf("without a session: %d", resp.StatusCode)
	}
}
