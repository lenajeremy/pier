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
	"testing"

	"pier/internal/auth"
	"pier/internal/deploy"
	"pier/internal/store"
)

func newServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "pier.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hash, _ := auth.HashPassword("correct horse battery")
	if _, err := st.CreateUser(context.Background(), "owner@example.com", "Owner", hash); err != nil {
		t.Fatal(err)
	}
	a := &API{
		Store: st, Token: "pier_server_token", Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Deployer: &deploy.Deployer{
			Store: st, Domain: "pier.localhost", PierHost: "pier.localhost", ProductionBranch: "main",
			URL: func(h string) string { return "http://" + h },
		},
	}
	srv := httptest.NewServer(a.Public())
	t.Cleanup(srv.Close)
	return srv, st
}

func do(t *testing.T, c *http.Client, method, url, body string, pierHeader bool) (*http.Response, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	if pierHeader {
		req.Header.Set("X-Pier", "1")
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

func TestSignInSessionSignOut(t *testing.T) {
	srv, _ := newServer(t)
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	creds := `{"email":"owner@example.com","password":"correct horse battery"}`

	if resp, _ := do(t, c, "GET", srv.URL+"/api/session", "", false); resp.StatusCode != 401 {
		t.Fatalf("signed out: got %d", resp.StatusCode)
	}
	if resp, _ := do(t, c, "POST", srv.URL+"/api/session", creds, false); resp.StatusCode != 403 {
		t.Fatalf("sign-in without X-Pier: got %d", resp.StatusCode)
	}
	resp, body := do(t, c, "POST", srv.URL+"/api/session", `{"email":"owner@example.com","password":"nope"}`, true)
	if resp.StatusCode != 401 || body["tries_left"] != float64(auth.Tries-1) {
		t.Fatalf("wrong password: got %d %v", resp.StatusCode, body)
	}
	resp, body = do(t, c, "POST", srv.URL+"/api/session", creds, true)
	if resp.StatusCode != 200 {
		t.Fatalf("sign in: got %d %v", resp.StatusCode, body)
	}
	if user, _ := body["user"].(map[string]any); user["email"] != "owner@example.com" {
		t.Fatalf("sign in answered %v", body)
	}
	if resp, body := do(t, c, "GET", srv.URL+"/api/session", "", false); resp.StatusCode != 200 || body["server"] == nil {
		t.Fatalf("session: got %d %v", resp.StatusCode, body)
	}
	if resp, _ := do(t, c, "GET", srv.URL+"/api/projects", "", false); resp.StatusCode != 200 {
		t.Fatalf("projects: got %d", resp.StatusCode)
	}
	if resp, _ := do(t, c, "DELETE", srv.URL+"/api/session", "", false); resp.StatusCode != 403 {
		t.Fatalf("sign out without X-Pier: got %d", resp.StatusCode)
	}
	if resp, _ := do(t, c, "DELETE", srv.URL+"/api/session", "", true); resp.StatusCode != 204 {
		t.Fatalf("sign out: got %d", resp.StatusCode)
	}
	if resp, _ := do(t, c, "GET", srv.URL+"/api/session", "", false); resp.StatusCode != 401 {
		t.Fatalf("after sign out: got %d", resp.StatusCode)
	}
}

func TestServerTokenStillWorks(t *testing.T) {
	srv, _ := newServer(t)
	req, _ := http.NewRequest("GET", srv.URL+"/api/board", nil)
	req.Header.Set("Authorization", "Bearer pier_server_token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("got %d", resp.StatusCode)
	}
}

func TestResetSignsEveryoneOut(t *testing.T) {
	srv, st := newServer(t)
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	do(t, c, "POST", srv.URL+"/api/session", `{"email":"owner@example.com","password":"correct horse battery"}`, true)
	owner, _ := st.Owner(context.Background())
	if n, err := st.DeleteSessions(context.Background(), owner.ID, ""); err != nil || n != 1 {
		t.Fatalf("deleted %d sessions, %v", n, err)
	}
	if resp, _ := do(t, c, "GET", srv.URL+"/api/session", "", false); resp.StatusCode != 401 {
		t.Fatalf("after reset: got %d", resp.StatusCode)
	}
}

func TestBrowserName(t *testing.T) {
	mac := "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	if got := browserName(mac); got != "Chrome on macOS" {
		t.Errorf("got %q", got)
	}
	iphone := "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1"
	if got := browserName(iphone); got != "Safari on iPhone" {
		t.Errorf("got %q", got)
	}
}
