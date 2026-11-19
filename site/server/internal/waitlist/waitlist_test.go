package waitlist

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNormalizeEmail(t *testing.T) {
	cases := []struct {
		in, want, err string
	}{
		{in: "  Sam@HomeLab.dev ", want: "sam@homelab.dev"},
		{in: "jo+pier@mail.example.co.uk", want: "jo+pier@mail.example.co.uk"},
		{in: "", err: "Enter your email address."},
		{in: "sam.homelab.dev", err: "missing an @"},
		{in: "@homelab.dev", err: "part before the @"},
		{in: "sam@homelab", err: "missing its domain"},
		{in: "sam@homelab.d", err: "missing its domain"},
		{in: "sam@.dev", err: "missing its domain"},
		{in: "sam smith@homelab.dev", err: "doesn't look like an email"},
		{in: strings.Repeat("a", 250) + "@x.dev", err: "too long"},
	}
	for _, c := range cases {
		got, err := NormalizeEmail(c.in)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("NormalizeEmail(%q) error = %v, want containing %q", c.in, err, c.err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("NormalizeEmail(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
}

func newHandler(t *testing.T) (*Handler, *Store) {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return &Handler{Store: store, Limiter: NewRateLimiter(100, time.Minute)}, store
}

func post(h http.Handler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/waitlist", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestJoinAlreadyAndInvalid(t *testing.T) {
	h, store := newHandler(t)

	if rec := post(h, `{"email":"Sam@homelab.dev"}`); rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"joined"`) {
		t.Fatalf("first join: %d %s", rec.Code, rec.Body)
	}
	if rec := post(h, `{"email":"sam@HOMELAB.dev"}`); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"already"`) {
		t.Fatalf("second join: %d %s", rec.Code, rec.Body)
	}
	if rec := post(h, `{"email":"sam@homelab"}`); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "missing its domain") {
		t.Fatalf("invalid: %d %s", rec.Code, rec.Body)
	}

	all, err := store.All(context.Background())
	if err != nil || len(all) != 1 || all[0].Email != "sam@homelab.dev" {
		t.Fatalf("stored = %+v, %v", all, err)
	}
}

func TestHoneypotStoresNothing(t *testing.T) {
	h, store := newHandler(t)
	if rec := post(h, `{"email":"bot@spam.dev","website":"http://spam.dev"}`); rec.Code != http.StatusCreated {
		t.Fatalf("honeypot: %d", rec.Code)
	}
	if all, _ := store.All(context.Background()); len(all) != 0 {
		t.Fatalf("honeypot stored %d entries", len(all))
	}
}

func TestRejectsNonJSONAndRateLimits(t *testing.T) {
	h, _ := newHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/waitlist", strings.NewReader("email=a@b.dev"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("form post: %d", rec.Code)
	}

	h.Limiter = NewRateLimiter(2, time.Minute)
	post(h, `{"email":"a@b.dev"}`)
	post(h, `{"email":"a@b.dev"}`)
	if rec := post(h, `{"email":"a@b.dev"}`); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("third attempt: %d, want 429", rec.Code)
	}
}

func TestClientIPOnlyTrustsLastProxyHop(t *testing.T) {
	h := &Handler{TrustProxy: true}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Forwarded-For", "6.6.6.6, 203.0.113.9")
	if ip := h.clientIP(req); ip != "203.0.113.9" {
		t.Fatalf("clientIP = %q", ip)
	}
	h.TrustProxy = false
	if ip := h.clientIP(req); ip != "192.0.2.1" { // httptest's RemoteAddr
		t.Fatalf("clientIP without proxy = %q", ip)
	}
}
