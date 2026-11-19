package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"pier/internal/build"
	"pier/internal/store"
)

const appSetting = "github_app"

type pendingManifest struct {
	State   string
	Hook    string
	Expires time.Time
}

func (a *API) githubRoutes(m *http.ServeMux) {
	for route, fn := range map[string]http.HandlerFunc{
		"GET /api/github": a.ghInfo, "POST /api/github/manifest": a.ghManifest, "GET /github/callback": a.ghCallback,
		"GET /api/github/repositories": a.ghList, "POST /api/github/import": a.ghImport,
		"POST /api/github/jobs/{id}/resume": a.ghResume,
	} {
		m.Handle(route, a.auth(fn))
	}
	m.HandleFunc("POST /github/events", a.ghEvent)
}

func (a *API) loadApp(ctx context.Context) (githubApp, error) {
	var app githubApp
	sealed, err := a.Store.Setting(ctx, appSetting)
	if err != nil || sealed == "" {
		return app, err
	}
	raw, err := a.Deployer.Secrets.Open(sealed, appSetting)
	if err != nil {
		return app, err
	}
	err = json.Unmarshal([]byte(raw), &app)
	return app, err
}
func githubError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func (a *API) ghInfo(w http.ResponseWriter, r *http.Request) {
	app, err := a.loadApp(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	jobs, err := a.Store.GitHubAttention(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"slug": app.Slug, "hook": app.Hook, "jobs": jobs})
}

func (a *API) ghManifest(w http.ResponseWriter, r *http.Request) {
	var req struct{ Hook string }
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil {
		githubError(w, 400, "Enter the public HTTPS webhook URL.")
		return
	}
	hook, err := url.Parse(req.Hook)
	if err != nil || hook.Scheme != "https" || hook.Hostname() == "" || hook.User != nil || hook.RawQuery != "" || hook.Fragment != "" || hook.Path != "/github/events" {
		githubError(w, 400, "Use a public HTTPS URL ending in /github/events.")
		return
	}
	app, err := a.loadApp(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	if app.ID != 0 {
		githubError(w, 409, "A GitHub App is already connected.")
		return
	}
	origin, err := url.Parse(r.Header.Get("Origin"))
	if err != nil || origin.Host != r.Host || origin.User != nil || origin.Path != "" || (origin.Scheme != "https" && !(origin.Scheme == "http" && (origin.Hostname() == "localhost" || origin.Hostname() == "127.0.0.1" || strings.HasSuffix(origin.Hostname(), ".localhost")))) {
		githubError(w, 400, "Open the dashboard over HTTPS or on localhost to register the App.")
		return
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		a.fail(w, err)
		return
	}
	state := hex.EncodeToString(nonce)
	// The cookie binds GitHub's return to the browser that started registration.
	http.SetCookie(w, &http.Cookie{Name: "pier_github_setup", Value: state, Path: "/github/callback", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: origin.Scheme == "https", MaxAge: 3600})
	pending, _ := json.Marshal(pendingManifest{state, hook.String(), time.Now().Add(time.Hour)})
	if err := a.Store.SetSetting(r.Context(), "github_setup", string(pending)); err != nil {
		a.fail(w, err)
		return
	}
	base := origin.String()
	writeJSON(w, 200, map[string]any{"action": "https://github.com/settings/apps/new?state=" + state, "manifest": map[string]any{
		"url": base, "public": false, "redirect_url": base + "/github/callback", "setup_url": base + "/integrations",
		"hook_attributes": map[string]any{"url": hook.String(), "active": true}, "default_events": []string{"push", "pull_request"},
		"default_permissions": map[string]string{"contents": "read", "pull_requests": "write", "statuses": "write"},
	}})
}
func (a *API) ghCallback(w http.ResponseWriter, r *http.Request) {
	a.githubSetup.Lock()
	defer a.githubSetup.Unlock()
	ctx := r.Context()
	c, err := r.Cookie("pier_github_setup")
	q := r.URL.Query()
	state := q.Get("state")
	code := q.Get("code")
	if err != nil || len(state) != 64 || !hmac.Equal([]byte(c.Value), []byte(state)) || code == "" {
		githubError(w, 400, "Start the GitHub connection again in Integrations.")
		return
	}
	rawSetup, err := a.Store.Setting(ctx, "github_setup")
	if err != nil {
		a.fail(w, err)
		return
	}
	current, err := a.loadApp(ctx)
	if err != nil {
		a.fail(w, err)
		return
	}
	var pending pendingManifest
	if json.Unmarshal([]byte(rawSetup), &pending) != nil || pending.State != state || time.Now().After(pending.Expires) || current.ID != 0 {
		githubError(w, 409, "This setup has already finished.")
		return
	}
	var app githubApp
	if err := a.ghJSON(ctx, "POST", "/app-manifests/"+url.PathEscape(code)+"/conversions", "", nil, &app); err != nil {
		githubError(w, 502, err.Error())
		return
	}
	if app.ID == 0 || app.Secret == "" || app.Key == "" || app.Slug == "" {
		githubError(w, 502, "GitHub returned an incomplete App registration.")
		return
	}
	app.Hook = pending.Hook
	raw, _ := json.Marshal(app)
	sealed, err := a.Deployer.Secrets.Seal(string(raw), appSetting)
	if err == nil {
		err = a.Store.SetSetting(ctx, appSetting, sealed)
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: c.Name, Value: "", Path: "/github/callback", MaxAge: -1, HttpOnly: true})
	http.Redirect(w, r, "https://github.com/apps/"+url.PathEscape(app.Slug)+"/installations/new", http.StatusSeeOther)
}
func (a *API) ghList(w http.ResponseWriter, r *http.Request) {
	app, err := a.loadApp(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	if app.ID == 0 {
		writeJSON(w, 200, map[string]any{"repositories": []githubRepo{}})
		return
	}
	repos, err := a.ghRepos(r.Context(), app)
	if err != nil {
		githubError(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"repositories": repos})
}
func (a *API) ghImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name               string
		Repo, Installation int64
		Branch             string
		Build              store.Build
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&req) != nil || !validProject(req.Name) || a.reserved(req.Name) || req.Repo <= 0 || req.Installation <= 0 {
		githubError(w, 400, "Choose a repository and a valid project name.")
		return
	}
	if _, err := build.Folder(req.Build.Root); err != nil {
		githubError(w, 400, "Choose a root directory inside the project.")
		return
	}
	if _, err := build.Folder(req.Build.Output); err != nil {
		githubError(w, 400, "Choose an output folder inside the project.")
		return
	}
	if req.Build.Preset != "" && req.Build.Preset != "vite" && req.Build.Preset != "static" {
		githubError(w, 400, "Choose a supported build preset.")
		return
	}
	ctx := r.Context()
	app, err := a.loadApp(ctx)
	if err != nil {
		a.fail(w, err)
		return
	}
	if app.ID == 0 {
		githubError(w, 409, "Connect GitHub first.")
		return
	}
	token, err := a.ghToken(ctx, app, req.Installation, req.Repo)
	if err != nil {
		githubError(w, 502, err.Error())
		return
	}
	repo, err := a.ghRepo(ctx, token, req.Repo)
	if err != nil {
		githubError(w, 502, err.Error())
		return
	}
	if req.Branch == "" {
		req.Branch = repo.Branch
	}
	sha, err := a.ghHead(ctx, token, repo.Name, req.Branch, 0)
	if err != nil || !commitSHA.MatchString(sha) {
		githubError(w, 422, "GitHub could not find a commit on that branch.")
		return
	}
	err = a.Store.ImportGitHub(ctx, store.Project{Name: req.Name, Repo: repo.Name, ProductionBranch: req.Branch, Build: req.Build}, repo.ID, req.Installation, sha)
	if errors.Is(err, store.ErrExists) || err != nil && strings.Contains(err.Error(), "UNIQUE") {
		githubError(w, 409, "This project name or repository is already in use.")
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 201, map[string]string{"name": req.Name})
}

func (a *API) ghEvent(w http.ResponseWriter, r *http.Request) {
	app, err := a.loadApp(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 25<<20))
	if err != nil {
		githubError(w, 413, "Delivery too large.")
		return
	}
	mac := hmac.New(sha256.New, []byte(app.Secret))
	mac.Write(body)
	sig, _ := hex.DecodeString(strings.TrimPrefix(r.Header.Get("X-Hub-Signature-256"), "sha256="))
	if app.Secret == "" || !strings.HasPrefix(r.Header.Get("X-Hub-Signature-256"), "sha256=") || !hmac.Equal(sig, mac.Sum(nil)) {
		githubError(w, 401, "Invalid delivery signature.")
		return
	}
	var e struct {
		Action, Ref, After       string
		Deleted                  bool
		Number                   int
		Installation, Repository struct{ ID int64 }
		Pull                     struct {
			Head struct {
				SHA  string
				Repo struct{ ID int64 }
			}
			Base struct{ Repo struct{ ID int64 } }
		} `json:"pull_request"`
	}
	if json.Unmarshal(body, &e) != nil {
		githubError(w, 400, "Invalid delivery.")
		return
	}
	delivery := r.Header.Get("X-GitHub-Delivery")
	if delivery == "" || len(delivery) > 128 {
		githubError(w, 400, "Missing delivery ID.")
		return
	}
	var branch, sha string
	var pr int
	var approval bool
	switch r.Header.Get("X-GitHub-Event") {
	case "push":
		if e.Deleted || !strings.HasPrefix(e.Ref, "refs/heads/") {
			w.WriteHeader(204)
			return
		}
		branch = strings.TrimPrefix(e.Ref, "refs/heads/")
		sha = e.After
	case "pull_request":
		if e.Action != "opened" && e.Action != "synchronize" && e.Action != "reopened" {
			w.WriteHeader(204)
			return
		}
		if e.Number <= 0 || e.Pull.Base.Repo.ID != e.Repository.ID || e.Pull.Head.Repo.ID == 0 {
			githubError(w, 400, "Invalid pull request.")
			return
		}
		pr = e.Number
		branch = fmt.Sprintf("pr-%d", pr)
		sha = e.Pull.Head.SHA
		approval = e.Pull.Head.Repo.ID != e.Repository.ID
	default:
		w.WriteHeader(204)
		return
	}
	if !commitSHA.MatchString(sha) || branch == "" || e.Repository.ID <= 0 || e.Installation.ID <= 0 {
		githubError(w, 400, "Invalid commit or repository.")
		return
	}
	if err := a.Store.QueueGitHub(r.Context(), e.Repository.ID, e.Installation.ID, delivery, branch, sha, pr, approval); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(202)
}
func (a *API) ghResume(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	err := a.Store.ResumeGitHub(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		githubError(w, 409, "This delivery is no longer waiting.")
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"queued": true})
}
