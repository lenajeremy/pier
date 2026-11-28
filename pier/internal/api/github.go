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
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"pier/internal/build"
	"pier/internal/store"
)

// The stored App, sealed, and the registration waiting for GitHub's return.
const (
	appSetting   = "github_app"
	setupSetting = "github_setup"
)

// maxDelivery is the largest delivery GitHub sends.
const maxDelivery = 25 << 20

// spool writes a delivery to disk and remembers whether the disk failed,
// which is pierd's fault rather than the sender's.
type spool struct {
	f   *os.File
	err error
}

func (s *spool) Write(p []byte) (int, error) {
	n, err := s.f.Write(p)
	if err != nil {
		s.err = err
	}
	return n, err
}

type pendingManifest struct {
	State   string
	Hook    string
	Expires time.Time
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
	if err := a.Store.SetSetting(r.Context(), setupSetting, string(pending)); err != nil {
		a.fail(w, err)
		return
	}
	base := origin.String()
	writeJSON(w, 200, map[string]any{"action": "https://github.com/settings/apps/new?state=" + state, "manifest": map[string]any{
		"url": base, "public": false, "redirect_url": base + "/github/callback", "setup_url": base + "/integrations/github",
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
	rawSetup, err := a.Store.Setting(ctx, setupSetting)
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
	a.event(ctx, "", "Connected GitHub App", app.Slug, "")
	http.Redirect(w, r, "https://github.com/apps/"+url.PathEscape(app.Slug)+"/installations/new", http.StatusSeeOther)
}

// ghDisconnect forgets the stored App so another can be registered. It is
// the way back when GitHub no longer honors the App's secret or key, or the
// App is gone. Projects keep their repositories, and deliveries that had not
// finished are dismissed. The App on GitHub is the owner's to delete.
func (a *API) ghDisconnect(w http.ResponseWriter, r *http.Request) {
	a.githubSetup.Lock()
	defer a.githubSetup.Unlock()
	ctx := r.Context()
	// A record that can't be opened is one more reason to disconnect.
	app, err := a.loadApp(ctx)
	if err != nil {
		a.Log.Warn("stored GitHub App can't be read", "err", err)
	}
	connected := app.ID != 0 || err != nil
	dismissed, err := a.Store.DisconnectGitHub(ctx, appSetting, setupSetting)
	if err != nil {
		a.fail(w, err)
		return
	}
	if connected {
		a.event(ctx, "", "Disconnected GitHub App", app.Slug, "")
	}
	writeJSON(w, 200, map[string]int64{"dismissed": dismissed})
}
func (a *API) ghList(w http.ResponseWriter, r *http.Request) {
	app, err := a.loadApp(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	if app.ID == 0 {
		writeJSON(w, 200, githubCatalog{Repositories: []githubRepo{}, Installations: []githubInstallation{}})
		return
	}
	catalog, err := a.ghRepos(r.Context(), app)
	if err != nil {
		githubError(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, catalog)
}

// ghActivity reads only App and delivery metadata, never delivery bodies.
func (a *API) ghActivity(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	app, err := a.loadApp(ctx)
	if err != nil {
		a.fail(w, err)
		return
	}
	if app.ID == 0 {
		githubError(w, 404, "Connect GitHub first.")
		return
	}
	jwt, err := app.jwt()
	if err != nil {
		a.fail(w, err)
		return
	}
	var meta struct {
		Created string `json:"created_at"`
	}
	if err := a.ghJSON(ctx, "GET", "/app", jwt, nil, &meta); err != nil {
		githubError(w, 502, err.Error())
		return
	}
	var deliveries []struct {
		At       string  `json:"delivered_at"`
		Event    string  `json:"event"`
		Status   int     `json:"status_code"`
		Duration float64 `json:"duration"`
	}
	if err := a.ghJSON(ctx, "GET", "/app/hook/deliveries?per_page=8", jwt, nil, &deliveries); err != nil {
		githubError(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"created_at": meta.Created, "deliveries": nonNil(deliveries)})
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
	if taken, err := a.Store.SubdomainTaken(r.Context(), req.Name); err != nil || taken {
		if err != nil {
			a.fail(w, err)
			return
		}
		githubError(w, 409, req.Name+" is another project's address. Choose another name.")
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
	if req.Build.Preset != "" && !slices.Contains(build.Presets, req.Build.Preset) {
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
		githubError(w, 409, "This project name is already in use.")
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
	rawSig, ok := strings.CutPrefix(r.Header.Get("X-Hub-Signature-256"), "sha256=")
	sig, decErr := hex.DecodeString(rawSig)
	if app.Secret == "" || !ok || decErr != nil || len(sig) != sha256.Size {
		githubError(w, 401, "Invalid delivery signature.")
		return
	}
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(10 * time.Second))
	// The body goes to disk as it arrives, through the signature check, so a
	// request that isn't from GitHub holds a small buffer whatever its size,
	// and only a signed one is decoded.
	if err = os.MkdirAll(a.Deployer.Tmp, 0o700); err != nil {
		a.fail(w, err)
		return
	}
	f, err := os.CreateTemp(a.Deployer.Tmp, "delivery-*.json")
	if err != nil {
		a.fail(w, err)
		return
	}
	defer func() {
		f.Close()
		os.Remove(f.Name())
	}()
	mac := hmac.New(sha256.New, []byte(app.Secret))
	disk := &spool{f: f}
	_, err = io.Copy(io.MultiWriter(mac, disk), http.MaxBytesReader(w, r.Body, maxDelivery))
	var tooLarge *http.MaxBytesError
	switch {
	case disk.err != nil:
		a.fail(w, disk.err)
		return
	case errors.As(err, &tooLarge):
		githubError(w, 413, "Delivery too large.")
		return
	case err != nil:
		githubError(w, 400, "Invalid delivery.")
		return
	}
	if !hmac.Equal(sig, mac.Sum(nil)) {
		githubError(w, 401, "Invalid delivery signature.")
		return
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		a.fail(w, err)
		return
	}
	var e struct {
		Action, Ref, After string
		Deleted            bool
		Number             int
		Installation       struct{ ID int64 }
		Repository         struct {
			ID   int64
			Name string `json:"full_name"`
		}
		Pull struct {
			Updated string `json:"updated_at"`
			Head    struct {
				SHA  string
				Repo struct{ ID int64 }
			}
			Base struct{ Repo struct{ ID int64 } }
		} `json:"pull_request"`
	}
	if json.NewDecoder(f).Decode(&e) != nil {
		githubError(w, 400, "Invalid delivery.")
		return
	}
	delivery := r.Header.Get("X-GitHub-Delivery")
	if delivery == "" || len(delivery) > 128 {
		githubError(w, 400, "Missing delivery ID.")
		return
	}
	// A deleted branch or a closed pull request has nothing to build. Its job
	// carries no commit, and retires the preview when its turn comes.
	var branch, sha string
	var pr int
	var approval, retire bool
	switch r.Header.Get("X-GitHub-Event") {
	case "push":
		if !strings.HasPrefix(e.Ref, "refs/heads/") {
			w.WriteHeader(204)
			return
		}
		branch = strings.TrimPrefix(e.Ref, "refs/heads/")
		sha = e.After
		retire = e.Deleted
	case "pull_request":
		retire = e.Action == "closed"
		if !retire && e.Action != "opened" && e.Action != "synchronize" && e.Action != "reopened" {
			w.WriteHeader(204)
			return
		}
		// The fork behind a closed pull request may be gone by now.
		if e.Number <= 0 || e.Pull.Base.Repo.ID != e.Repository.ID || e.Pull.Head.Repo.ID == 0 && !retire {
			githubError(w, 400, "Invalid pull request.")
			return
		}
		pr = e.Number
		branch = fmt.Sprintf("pr-%d", pr)
		sha = e.Pull.Head.SHA
		approval = !retire && e.Pull.Head.Repo.ID != e.Repository.ID
	default:
		w.WriteHeader(204)
		return
	}
	if retire {
		sha = ""
	}
	if !retire && !commitSHA.MatchString(sha) || branch == "" || e.Repository.ID <= 0 || e.Installation.ID <= 0 {
		githubError(w, 400, "Invalid commit or repository.")
		return
	}
	// GitHub's own time orders a pull request's deliveries, which can arrive
	// out of order. One it can't read sorts by arrival.
	updated, _ := time.Parse(time.RFC3339, e.Pull.Updated)
	projects, err := a.Store.QueueGitHub(r.Context(), e.Repository.ID, e.Installation.ID, delivery, branch, sha, pr, approval, updated)
	if err != nil {
		a.fail(w, err)
		return
	}
	// The App can be installed on repositories no project deploys. Say so,
	// here and to GitHub, so a delivery that does nothing can be told from
	// one that was queued.
	if len(projects) == 0 {
		a.Log.Info("GitHub delivery ignored: no project uses this repository", "repository", e.Repository.Name, "id", e.Repository.ID, "installation", e.Installation.ID, "delivery", delivery)
		w.WriteHeader(204)
		return
	}
	w.WriteHeader(202)
}

// ghDismiss ends a delivery that waits for the owner without building it.
func (a *API) ghDismiss(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	err := a.Store.DismissGitHub(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		githubError(w, 409, "This delivery is no longer waiting.")
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"dismissed": true})
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
