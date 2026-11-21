package api

import (
	"archive/tar"
	"bytes"
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
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"pier/internal/deploy"
	"pier/internal/router"
	"pier/internal/secrets"
	"pier/internal/store"
)

// hub is a GitHub that a test can steer: where branches and pull requests
// point and what fails. It keeps what Pier told it.
type hub struct {
	mu       sync.Mutex
	key      string            // the App's private key, the same for every App it registers
	apps     int               // how many Apps were registered; the latest has this ID
	heads    map[string]string // a branch, or pr-N, to its head; a missing one is deleted or closed
	tokens   []string          // the installation each token was issued for
	statuses []string          // each commit status posted
	contexts []string          // the context each of those was posted under
	comments []hubComment      // comments on pull requests
	posted   int               // comments created
	patched  int               // comments rewritten
	fail     func(r *http.Request) int
	tarball  func(w http.ResponseWriter, r *http.Request)
	trees    map[string][]string   // a commit's files; a missing commit has no tree
	clipped  bool                  // every tree says GitHub truncated it
	compares map[string]hubCompare // "base...head" to how they differ; a missing pair can't be compared
	details  []string              // each commit status's description
}

// hubCompare is how two commits differ, as GitHub's comparison says.
type hubCompare struct {
	Status string    `json:"status"`
	Files  []hubFile `json:"files"`
}

type hubFile struct {
	Name     string `json:"filename"`
	Previous string `json:"previous_filename,omitempty"`
}

type hubComment struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
	App  struct {
		ID int64 `json:"id"`
	} `json:"performed_via_github_app"`
}

func (h *hub) handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("POST /app-manifests/{code}/conversions", func(w http.ResponseWriter, r *http.Request) {
		h.apps++
		json.NewEncoder(w).Encode(githubApp{ID: int64(h.apps), Slug: fmt.Sprintf("pier-test-%d", h.apps), Key: h.key, Secret: fmt.Sprintf("hook-secret-%d", h.apps)})
	})
	m.HandleFunc("POST /app/installations/{id}/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		h.tokens = append(h.tokens, r.PathValue("id"))
		io.WriteString(w, `{"token":"installation-token"}`)
	})
	m.HandleFunc("GET /app/installations", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `[{"id":42,"account":{"login":"owner","type":"User"}}]`)
	})
	m.HandleFunc("GET /installation/repositories", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"repositories":[{"id":7,"full_name":"owner/site","default_branch":"main"},{"id":8,"full_name":"owner/other","default_branch":"main"}]}`)
	})
	m.HandleFunc("GET /repositories/7", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"id":7,"full_name":"owner/site","default_branch":"main"}`)
	})
	m.HandleFunc("GET /repos/owner/site/git/commits/{sha}", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"message":"Change the site"}`)
	})
	m.HandleFunc("GET /repos/owner/site/branches/{branch}", func(w http.ResponseWriter, r *http.Request) {
		sha, ok := h.heads[r.PathValue("branch")]
		if !ok {
			w.WriteHeader(404)
			return
		}
		fmt.Fprintf(w, `{"commit":{"sha":%q}}`, sha)
	})
	m.HandleFunc("GET /repos/owner/site/pulls/{n}", func(w http.ResponseWriter, r *http.Request) {
		state := "open"
		sha, ok := h.heads["pr-"+r.PathValue("n")]
		if !ok {
			state = "closed"
		}
		fmt.Fprintf(w, `{"state":%q,"head":{"sha":%q}}`, state, sha)
	})
	m.HandleFunc("GET /repos/owner/site/tarball/{sha}", func(w http.ResponseWriter, r *http.Request) {
		w.Write(siteArchive(r.PathValue("sha")))
	})
	m.HandleFunc("GET /repos/owner/site/git/trees/{sha}", func(w http.ResponseWriter, r *http.Request) {
		files, ok := h.trees[r.PathValue("sha")]
		if !ok || r.URL.Query().Get("recursive") != "1" {
			w.WriteHeader(404)
			return
		}
		tree := []map[string]string{}
		for _, f := range files {
			tree = append(tree, map[string]string{"path": f, "type": "blob"})
		}
		json.NewEncoder(w).Encode(map[string]any{"sha": r.PathValue("sha"), "tree": tree, "truncated": h.clipped})
	})
	m.HandleFunc("GET /repos/owner/site/compare/{basehead}", func(w http.ResponseWriter, r *http.Request) {
		c, ok := h.compares[r.PathValue("basehead")]
		if !ok {
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(c)
	})
	m.HandleFunc("POST /repos/owner/site/statuses/{sha}", func(w http.ResponseWriter, r *http.Request) {
		var msg struct{ State, Context, Description string }
		json.NewDecoder(r.Body).Decode(&msg)
		h.statuses = append(h.statuses, msg.State)
		h.contexts = append(h.contexts, msg.Context)
		h.details = append(h.details, msg.Description)
	})
	m.HandleFunc("GET /repos/owner/site/issues/{n}/comments", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(append([]hubComment{}, h.comments...))
	})
	m.HandleFunc("POST /repos/owner/site/issues/{n}/comments", func(w http.ResponseWriter, r *http.Request) {
		c := hubComment{ID: int64(len(h.comments) + 1)}
		json.NewDecoder(r.Body).Decode(&c)
		c.App.ID = int64(h.apps)
		h.comments = append(h.comments, c)
		h.posted++
	})
	m.HandleFunc("PATCH /repos/owner/site/issues/comments/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		for i := range h.comments {
			if h.comments[i].ID == id {
				json.NewDecoder(r.Body).Decode(&h.comments[i])
				h.patched++
				return
			}
		}
		w.WriteHeader(404)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		// A test's own download may take its time, so it runs unlocked.
		if tarball := h.tarball; tarball != nil && strings.Contains(r.URL.Path, "/tarball/") {
			h.mu.Unlock()
			tarball(w, r)
			return
		}
		defer h.mu.Unlock()
		if h.fail != nil {
			if code := h.fail(r); code != 0 {
				w.WriteHeader(code)
				return
			}
		}
		m.ServeHTTP(w, r)
	})
}

func (h *hub) lastStatus() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.statuses) == 0 {
		return ""
	}
	return h.statuses[len(h.statuses)-1]
}

// siteArchive is a GitHub source archive of a one-page site.
func siteArchive(sha string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Typeflag: tar.TypeXGlobalHeader, Name: "pax_global_header", PAXRecords: map[string]string{"comment": sha}})
	tw.WriteHeader(&tar.Header{Name: "owner-site-sha/index.html", Mode: 0o644, Size: int64(len(sha))})
	io.WriteString(tw, sha)
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

// stubRunner stands in for the build command. Each test decides what a
// build does; left alone, it succeeds at once.
type stubRunner struct {
	mu   sync.Mutex
	runs int
	run  func(ctx context.Context) error
}

func (*stubRunner) Name() string { return "a test" }

// build sets what the next builds do; nil lets them succeed.
func (s *stubRunner) build(run func(ctx context.Context) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.run = run
}

func (s *stubRunner) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runs
}
func (s *stubRunner) Run(ctx context.Context, dir, command string, env []string, log io.Writer) error {
	s.mu.Lock()
	s.runs++
	run := s.run
	s.mu.Unlock()
	if run == nil {
		return nil
	}
	return run(ctx)
}

// syncBuffer collects log lines written from more than one goroutine.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// ghEnv is a Pier with a connected App and one project, site, imported from
// repository 7 through installation 42 and already deployed to production.
type ghEnv struct {
	t      *testing.T
	st     *store.Store
	db     *sql.DB // a second connection, to read job rows the store has no call for
	a      *API
	hub    *hub
	runner *stubRunner
	secret string // the connected App's webhook secret
	logs   syncBuffer
	routes syncBuffer // the latest configuration the router was given
	serve  http.Handler
}

var ownerHeaders = map[string]string{"Authorization": "Bearer server", "Origin": "http://localhost"}

// appKey is one private key for every test's App: making one takes a while.
var appKey = sync.OnceValue(func() string {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
})

func newGitHubEnv(t *testing.T) *ghEnv {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "pier.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "pier.db")+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	box, err := secrets.Load(filepath.Join(dir, "key"))
	if err != nil {
		t.Fatal(err)
	}
	env := &ghEnv{t: t, st: st, db: db, runner: &stubRunner{}}
	env.hub = &hub{key: appKey(), heads: map[string]string{"main": strings.Repeat("a", 40)}}
	remote := httptest.NewServer(env.hub.handler())
	t.Cleanup(remote.Close)
	caddy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		env.routes.mu.Lock()
		env.routes.buf.Reset()
		env.routes.buf.Write(body)
		env.routes.mu.Unlock()
	}))
	t.Cleanup(caddy.Close)
	log := slog.New(slog.NewTextHandler(&env.logs, nil))
	env.a = &API{Store: st, Token: "server", Log: log, githubEndpoint: remote.URL, Deployer: &deploy.Deployer{
		Store: st, Secrets: box, Domain: "pier.test", PierHost: "pier.test", ProductionBranch: "main",
		Tmp: filepath.Join(dir, "tmp"), Sources: filepath.Join(dir, "sources"), Deployments: filepath.Join(dir, "deployments"), Logs: filepath.Join(dir, "logs"),
		Log: log, BuildTimeout: time.Minute, Runner: env.runner, Router: router.Caddy{Admin: caddy.URL},
		URL: func(h string) string { return "https://" + h },
	}}
	env.serve = env.a.Public()
	env.connect()
	// A build command sends every build through the runner.
	if w := env.call("POST", "/api/github/import", `{"name":"site","repo":7,"installation":42,"build":{"command":"make","output":"."}}`, ownerHeaders); w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	env.run(ctx)
	return env
}

func (e *ghEnv) call(method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	e.serve.ServeHTTP(w, req)
	return w
}

// connect registers an App through the manifest flow, as the owner would.
func (e *ghEnv) connect() {
	e.t.Helper()
	setup := e.call("POST", "/api/github/manifest", `{"hook":"https://pier.test/github/events"}`, ownerHeaders)
	if setup.Code != 200 {
		e.t.Fatalf("manifest %d %s", setup.Code, setup.Body)
	}
	cookie := setup.Result().Cookies()[0]
	back := e.call("GET", "/github/callback?code=one-time-code&state="+cookie.Value, "", map[string]string{"Authorization": "Bearer server", "Cookie": cookie.String()})
	if back.Code != 303 {
		e.t.Fatalf("callback %d %s", back.Code, back.Body)
	}
	e.secret = fmt.Sprintf("hook-secret-%d", e.hub.apps)
}

// event delivers a webhook signed with the connected App's secret.
func (e *ghEnv) event(kind, id, body string) int {
	e.t.Helper()
	mac := hmac.New(sha256.New, []byte(e.secret))
	io.WriteString(mac, body)
	return e.call("POST", "/github/events", body, map[string]string{"X-GitHub-Event": kind, "X-GitHub-Delivery": id, "X-Hub-Signature-256": "sha256=" + hex.EncodeToString(mac.Sum(nil))}).Code
}

// push moves a branch on GitHub and delivers the event, which must be queued.
func (e *ghEnv) push(id, branch, sha string) {
	e.t.Helper()
	e.hub.mu.Lock()
	e.hub.heads[branch] = sha
	e.hub.mu.Unlock()
	body := fmt.Sprintf(`{"installation":{"id":42},"repository":{"id":7,"full_name":"owner/site"},"ref":"refs/heads/%s","after":%q}`, branch, sha)
	if code := e.event("push", id, body); code != 202 {
		e.t.Fatalf("push answered %d", code)
	}
}

// pull delivers a pull request event for number 9, from the repository
// itself or from a fork, and moves the pull request to match.
func (e *ghEnv) pull(id, action, sha string, fork bool) {
	e.t.Helper()
	e.hub.mu.Lock()
	if e.hub.heads["pr-9"] = sha; action == "closed" {
		delete(e.hub.heads, "pr-9")
	}
	e.hub.mu.Unlock()
	head := 7
	if fork {
		head = 8
	}
	body := fmt.Sprintf(`{"action":%q,"number":9,"installation":{"id":42},"repository":{"id":7},"pull_request":{"base":{"repo":{"id":7}},"head":{"sha":%q,"repo":{"id":%d}}}}`, action, sha, head)
	if code := e.event("pull_request", id, body); code != 202 {
		e.t.Fatalf("pull request event answered %d", code)
	}
}

func (e *ghEnv) next(ctx context.Context) store.GitHubJob {
	e.t.Helper()
	j, err := e.st.NextGitHub(ctx)
	if err != nil {
		e.t.Fatal("no queued job: ", err)
	}
	return j
}

// run works the next queued job, which must not fail.
func (e *ghEnv) run(ctx context.Context) store.GitHubJob {
	e.t.Helper()
	j := e.next(ctx)
	if err := e.a.workGitHub(ctx, &j); err != nil {
		e.t.Fatal(err)
	}
	return j
}

func (e *ghEnv) idle() {
	e.t.Helper()
	if j, err := e.st.NextGitHub(context.Background()); !errors.Is(err, sql.ErrNoRows) {
		e.t.Fatalf("unexpected queued job %+v: %v", j, err)
	}
}

// job reads a job's state, deployment and detail.
func (e *ghEnv) job(id int64) (state, deployment, detail string) {
	e.t.Helper()
	if err := e.db.QueryRow("SELECT state,deployment,detail FROM github_jobs WHERE id=?", id).Scan(&state, &deployment, &detail); err != nil {
		e.t.Fatal(err)
	}
	return
}

// waiting lists the deliveries that wait for the owner.
func (e *ghEnv) waiting() []store.GitHubWaiting {
	e.t.Helper()
	jobs, err := e.st.GitHubAttention(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	return jobs
}

// aliases maps every address to the deployment it serves.
func (e *ghEnv) aliases() map[string]string {
	e.t.Helper()
	list, err := e.st.Aliases(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	out := map[string]string{}
	for _, a := range list {
		out[a.Host] = a.DeploymentID
	}
	return out
}
