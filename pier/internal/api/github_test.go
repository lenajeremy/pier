package api

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pier/internal/build"
	"pier/internal/deploy"
	"pier/internal/router"
	"pier/internal/secrets"
	"pier/internal/store"
)

func TestGitHubDeploymentFlow(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "pier.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	box, err := secrets.Load(filepath.Join(dir, "key"))
	if err != nil {
		t.Fatal(err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	app := githubApp{ID: 1, Slug: "pier-test", Key: string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})), Secret: "test-webhook"}
	sha := strings.Repeat("a", 40)
	other := strings.Repeat("b", 40)
	lastStatus := ""
	comments := 0
	badBuild := false
	fake := http.NewServeMux()
	fake.HandleFunc("POST /app-manifests/one-time-code/conversions", func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(app) })
	fake.HandleFunc("POST /app/installations/42/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		if len(strings.Split(r.Header.Get("Authorization"), ".")) != 3 {
			t.Error("missing App JWT")
		}
		io.WriteString(w, `{"token":"installation-token"}`)
	})
	fake.HandleFunc("GET /repositories/7", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer installation-token" {
			t.Error("missing installation token")
		}
		io.WriteString(w, `{"id":7,"full_name":"owner/site","default_branch":"main"}`)
	})
	fake.HandleFunc("GET /repos/owner/site/git/commits/{sha}", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"message": "Add home page\n\nDetails"})
	})
	fake.HandleFunc("GET /repos/owner/site/branches/{branch}", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintf(w, `{"commit":{"sha":%q}}`, sha) })
	fake.HandleFunc("GET /repos/owner/site/pulls/9", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"state":"open","head":{"sha":%q}}`, other)
	})
	fake.HandleFunc("GET /repos/owner/site/tarball/{sha}", func(w http.ResponseWriter, r *http.Request) {
		gz := gzip.NewWriter(w)
		tw := tar.NewWriter(gz)
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeXGlobalHeader, Name: "pax_global_header", PAXRecords: map[string]string{"comment": r.PathValue("sha")}}); err != nil {
			t.Error(err)
		}
		name := "index.html"
		if badBuild {
			name = "README.md"
		}
		body := r.PathValue("sha")
		tw.WriteHeader(&tar.Header{Name: "owner-site-sha/" + name, Mode: 0644, Size: int64(len(body))})
		io.WriteString(tw, body)
		tw.Close()
		gz.Close()
	})
	fake.HandleFunc("POST /repos/owner/site/statuses/{sha}", func(w http.ResponseWriter, r *http.Request) {
		var msg struct{ State string }
		json.NewDecoder(r.Body).Decode(&msg)
		lastStatus = msg.State
	})
	fake.HandleFunc("GET /repos/owner/site/issues/9/comments", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "[]") })
	fake.HandleFunc("POST /repos/owner/site/issues/9/comments", func(w http.ResponseWriter, r *http.Request) {
		var msg struct{ Body string }
		json.NewDecoder(r.Body).Decode(&msg)
		if !strings.Contains(msg.Body, ".pier.test") {
			t.Error("missing preview link")
		}
		comments++
	})
	remote := httptest.NewServer(fake)
	defer remote.Close()
	caddy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer caddy.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := API{Store: st, Token: "server", Log: log, githubEndpoint: remote.URL, Deployer: &deploy.Deployer{Store: st, Secrets: box, Domain: "pier.test", PierHost: "pier.test", ProductionBranch: "main", Tmp: filepath.Join(dir, "tmp"), Sources: filepath.Join(dir, "sources"), Deployments: filepath.Join(dir, "deployments"), Logs: filepath.Join(dir, "logs"), Log: log, BuildTimeout: time.Minute, Runner: build.Local{}, Router: router.Caddy{Admin: caddy.URL}, URL: func(h string) string { return "https://" + h }}}
	handler := a.Public()
	call := func(method, path, body string, h map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(body))
		for k, v := range h {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	owner := map[string]string{"Authorization": "Bearer server", "Origin": "http://localhost"}
	setup := call("POST", "/api/github/manifest", `{"hook":"https://pier.test/github/events"}`, owner)
	if setup.Code != 200 {
		t.Fatalf("manifest %d %s", setup.Code, setup.Body)
	}
	cookie := setup.Result().Cookies()[0]
	callback := func(state string) int {
		return call("GET", "/github/callback?code=one-time-code&state="+state, "", map[string]string{"Authorization": "Bearer server", "Cookie": cookie.String()}).Code
	}
	if callback("wrong") != 400 || callback(cookie.Value) != 303 {
		t.Fatal("invalid manifest callback handling")
	}
	sealed, _ := st.Setting(ctx, appSetting)
	if strings.Contains(sealed, app.Secret) || strings.Contains(sealed, "PRIVATE KEY") {
		t.Fatal("App credentials were stored in plaintext")
	}
	run := func() store.GitHubJob {
		t.Helper()
		j, err := st.NextGitHub(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err = a.workGitHub(ctx, &j); err != nil {
			t.Fatal(err)
		}
		return j
	}
	empty := func() {
		t.Helper()
		if _, err := st.NextGitHub(ctx); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("unexpected queued job: %v", err)
		}
	}
	if w := call("POST", "/api/github/import", `{"name":"site","repo":7,"installation":42}`, owner); w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	prod := run()
	first, err := st.Deployment(ctx, prod.Deployment)
	if err != nil || first.Message != "Add home page" {
		t.Fatalf("commit subject %q, %v", first.Message, err)
	}
	if lastStatus != "success" {
		t.Fatal(lastStatus)
	}
	content, err := os.ReadFile(filepath.Join(a.Deployer.Deployments, prod.Deployment, "index.html"))
	if err != nil || string(content) != sha {
		t.Fatal("production contents", err)
	}
	event := func(kind, id, body, secret string) int {
		mac := hmac.New(sha256.New, []byte(secret))
		io.WriteString(mac, body)
		return call("POST", "/github/events", body, map[string]string{"X-GitHub-Event": kind, "X-GitHub-Delivery": id, "X-Hub-Signature-256": "sha256=" + hex.EncodeToString(mac.Sum(nil))}).Code
	}
	push := fmt.Sprintf(`{"installation":{"id":42},"repository":{"id":7},"ref":"refs/heads/feature","after":%q}`, sha)
	if event("push", "p1", push, "wrong") != 401 {
		t.Fatal("unsigned delivery accepted")
	}
	empty()
	if event("push", "p1", push, app.Secret) != 202 || event("push", "p1", push, app.Secret) != 202 {
		t.Fatal("push rejected")
	}
	branch := run()
	empty()
	pull := fmt.Sprintf(`{"action":"opened","number":9,"installation":{"id":42},"repository":{"id":7},"pull_request":{"base":{"repo":{"id":7}},"head":{"sha":%q,"repo":{"id":8}}}}`, other)
	if event("pull_request", "pr1", pull, app.Secret) != 202 {
		t.Fatal("PR rejected")
	}
	empty()
	jobs, err := st.GitHubAttention(ctx)
	if err != nil || len(jobs) != 1 || jobs[0].SHA != other {
		t.Fatal("missing fork approval", err)
	}
	path := fmt.Sprintf("/api/github/jobs/%d/resume", jobs[0].ID)
	if call("POST", path, "", nil).Code != 401 {
		t.Fatal("anonymous approval")
	}
	if call("POST", path, "", owner).Code != 200 {
		t.Fatal("owner approval failed")
	}
	pr := run()
	if comments != 1 {
		t.Fatal("missing preview comment")
	}
	badBuild = true
	sha = strings.Repeat("c", 40)
	failed := fmt.Sprintf(`{"installation":{"id":42},"repository":{"id":7},"ref":"refs/heads/main","after":%q}`, sha)
	event("push", "p2", failed, app.Secret)
	run()
	if lastStatus != "failure" {
		t.Fatal("missing failed build status")
	}
	aliases, err := st.Aliases(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"site.pier.test": prod.Deployment, "feature--site.pier.test": branch.Deployment, "pr-9--site.pier.test": pr.Deployment}
	for _, alias := range aliases {
		if want[alias.Host] != alias.DeploymentID {
			t.Fatalf("wrong alias %+v", alias)
		}
		delete(want, alias.Host)
	}
	if len(want) != 0 {
		t.Fatal("missing aliases", want)
	}
}
