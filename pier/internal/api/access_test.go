package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"pier/internal/alerts"
	"pier/internal/auth"
	"pier/internal/deploy"
	"pier/internal/jobs"
	"pier/internal/secrets"
	"pier/internal/store"
)

// decisions is what each route is open to, decided once, here, apart from
// the route table: "public" needs no account, "viewer" is anyone signed in,
// "member" is members and the owner, and "owner" is the owner alone. A route
// added without a line here fails TestEveryRouteHasARoleDecision.
var decisions = map[string]string{
	"GET /healthz":                        "public",
	"POST /api/session":                   "public",
	"POST /api/cli/login":                 "public",
	"POST /api/cli/login/poll":            "public",
	"POST /api/invites/lookup":            "public",
	"POST /api/invites/accept":            "public",
	"POST /github/events":                 "public",
	"GET /api/session":                    "viewer",
	"DELETE /api/session":                 "viewer",
	"GET /api/sessions":                   "viewer",
	"DELETE /api/sessions/others":         "viewer",
	"DELETE /api/sessions/{id}":           "viewer",
	"POST /api/settings/password":         "viewer",
	"GET /api/account/alerts":             "viewer",
	"PUT /api/account/alerts":             "viewer",
	"GET /api/cli/login/{code}":           "viewer",
	"POST /api/cli/login/{code}/{answer}": "viewer",

	"GET /api/version":                                         "viewer",
	"GET /api/board":                                           "viewer",
	"GET /api/projects":                                        "viewer",
	"GET /api/projects/{project}":                              "viewer",
	"GET /api/projects/{project}/favicon":                      "viewer",
	"GET /api/projects/{project}/settings":                     "viewer",
	"GET /api/projects/{project}/folders":                      "viewer",
	"GET /api/projects/{project}/deployments":                  "viewer",
	"GET /api/projects/{project}/deployments/{id}":             "viewer",
	"GET /api/projects/{project}/deployments/{id}/log":         "viewer",
	"GET /api/projects/{project}/deployments/{id}/runtime-log": "viewer",
	"GET /api/projects/{project}/variables":                    "viewer",
	"GET /api/projects/{project}/requests":                     "viewer",
	"GET /api/projects/{project}/requests/chart":               "viewer",
	"GET /api/projects/{project}/domains":                      "viewer",
	"GET /api/shared-variables":                                "viewer",
	"GET /api/activity":                                        "viewer",
	"GET /api/members":                                         "viewer",
	"GET /api/server":                                          "viewer",
	"GET /api/server/history":                                  "viewer",
	"GET /api/settings":                                        "viewer",
	"POST /api/settings/dns":                                   "viewer",
	"GET /api/github":                                          "viewer",
	"GET /api/github/activity":                                 "viewer",
	"GET /api/github/repositories":                             "viewer",
	"GET /api/github/repositories/{id}/folders":                "viewer",
	"GET /api/projects/{project}/database":                     "viewer",
	"GET /api/projects/{project}/storage":                      "viewer",
	"GET /api/cloudflare":                                      "viewer",
	"GET /api/projects/{project}/jobs":                         "viewer",
	"GET /api/projects/{project}/jobs/{id}":                    "viewer",
	"GET /api/projects/{project}/jobs/{id}/runs":               "viewer",
	"GET /api/projects/{project}/jobs/{id}/runs/{run}":         "viewer",
	"POST /api/jobs/schedule":                                  "viewer",

	"POST /api/projects":                                               "member",
	"DELETE /api/projects/{project}":                                   "member",
	"PUT /api/projects/{project}/settings":                             "member",
	"PUT /api/projects/{project}/address":                              "member",
	"POST /api/projects/{project}/deployments":                         "member",
	"POST /api/projects/{project}/redeploy":                            "member",
	"POST /api/projects/{project}/promote":                             "member",
	"POST /api/projects/{project}/rollback":                            "member",
	"POST /api/projects/{project}/rollback/undo":                       "member",
	"POST /api/projects/{project}/deployments/{id}/restart":            "member",
	"PUT /api/projects/{project}/variables":                            "member",
	"POST /api/projects/{project}/variables/import":                    "member",
	"POST /api/projects/{project}/variables/delete":                    "member",
	"POST /api/projects/{project}/variables/reveal":                    "member",
	"POST /api/projects/{project}/domains":                             "member",
	"POST /api/projects/{project}/domains/{host}/check":                "member",
	"DELETE /api/projects/{project}/domains/{host}":                    "member",
	"POST /api/shared-variables":                                       "member",
	"PUT /api/shared-variables/{name}":                                 "member",
	"DELETE /api/shared-variables/{name}":                              "member",
	"POST /api/github/import":                                          "member",
	"POST /api/github/jobs/{id}/resume":                                "member",
	"POST /api/github/jobs/{id}/dismiss":                               "member",
	"GET /api/projects/{project}/database/url":                         "member",
	"GET /api/projects/{project}/database/tables":                      "member",
	"GET /api/projects/{project}/database/tables/{schema}/{table}":     "member",
	"POST /api/projects/{project}/database/query":                      "member",
	"GET /api/projects/{project}/database/tunnel":                      "member",
	"POST /api/projects/{project}/database":                            "member",
	"PUT /api/projects/{project}/database":                             "member",
	"DELETE /api/projects/{project}/database":                          "member",
	"POST /api/projects/{project}/database/restart":                    "member",
	"POST /api/projects/{project}/database/backups":                    "member",
	"GET /api/projects/{project}/database/backups/{id}/file":           "member",
	"POST /api/projects/{project}/database/backups/{id}/restore":       "member",
	"POST /api/projects/{project}/database/databases":                  "member",
	"PUT /api/projects/{project}/database/databases/{name}":            "member",
	"DELETE /api/projects/{project}/database/databases/{name}":         "member",
	"POST /api/projects/{project}/database/databases/{name}/externals": "member",
	"GET /api/projects/{project}/database/externals/{role}":            "member",
	"DELETE /api/projects/{project}/database/externals/{role}":         "member",
	"GET /api/projects/{project}/storage/{bucket}/variables":           "member",
	"GET /api/projects/{project}/storage/{bucket}/files":               "member",
	"GET /api/projects/{project}/storage/{bucket}/file":                "member",
	"PUT /api/projects/{project}/storage/{bucket}/file":                "member",
	"DELETE /api/projects/{project}/storage/{bucket}/files":            "member",
	"POST /api/projects/{project}/storage":                             "member",
	"PUT /api/projects/{project}/storage/{bucket}":                     "member",
	"DELETE /api/projects/{project}/storage/{bucket}":                  "member",
	"POST /api/projects/{project}/jobs":                                "member",
	"PUT /api/projects/{project}/jobs/{id}":                            "member",
	"DELETE /api/projects/{project}/jobs/{id}":                         "member",
	"POST /api/projects/{project}/jobs/{id}/run":                       "member",

	"PUT /api/settings":               "owner",
	"PUT /api/cloudflare":             "owner",
	"DELETE /api/cloudflare":          "owner",
	"POST /api/server/cleanup/{kind}": "owner",
	"GET /api/server/upgrade":         "owner",
	"POST /api/server/upgrade":        "owner",
	"POST /api/github/manifest":       "owner",
	"GET /github/callback":            "owner",
	"DELETE /api/github":              "owner",
	"GET /api/alerts":                 "owner",
	"PUT /api/alerts":                 "owner",
	"PUT /api/alerts/{kind}":          "owner",
	"DELETE /api/alerts/{kind}":       "owner",
	"POST /api/alerts/{kind}/test":    "owner",
	"POST /api/alerts/webhook/secret": "owner",
	"PUT /api/members/{id}":           "owner",
	"DELETE /api/members/{id}":        "owner",
	"GET /api/invites":                "owner",
	"POST /api/invites":               "owner",
	"DELETE /api/invites/{id}":        "owner",
}

var accessNames = map[access]string{public: "public", viewer: "viewer", member: "member", owner: "owner"}

// team is a pierd with its owner, a member and a viewer, each signed in
// with a token of their own, the way pier login leaves a computer.
type team struct {
	srv                   *httptest.Server
	st                    *store.Store
	api                   *API
	owner, member, viewer string
	ids                   map[string]int64
}

func newTeam(t *testing.T, stub bool) *team {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "pier.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, err := secrets.Load(filepath.Join(t.TempDir(), "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	a := &API{
		Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Deployer: &deploy.Deployer{
			Store: st, Domain: "pier.localhost", PierHost: "pier.localhost", ProductionBranch: "main", Secrets: box,
			Logs: t.TempDir(), URL: func(h string) string { return "http://" + h },
		},
	}
	tm := &team{st: st, api: a, ids: map[string]int64{}}
	hash, _ := auth.HashPassword("correct horse battery")
	for _, who := range []string{"owner", "member", "viewer"} {
		name := strings.ToUpper(who[:1]) + who[1:]
		id, err := st.CreateUser(ctx, who+"@example.com", name, hash)
		if err != nil {
			t.Fatal(err)
		}
		if who == "member" {
			if _, err := st.SetRole(ctx, id, store.RoleMember); err != nil {
				t.Fatal(err)
			}
		}
		token, tokenHash, _ := auth.NewToken()
		se := store.Session{TokenHash: tokenHash, UserID: id, Kind: store.SessionCLI, Name: "pier CLI on test", ExpiresAt: time.Now().Add(time.Hour)}
		if err := st.CreateSession(ctx, se); err != nil {
			t.Fatal(err)
		}
		tm.ids[who] = id
		switch who {
		case "owner":
			tm.owner = token
		case "member":
			tm.member = token
		default:
			tm.viewer = token
		}
	}
	rs := a.routes()
	if stub {
		// Every handler is replaced by one that says it was reached, so
		// only the guard decides, and nothing really runs.
		for i := range rs {
			rs[i].handler = func(w http.ResponseWriter, r *http.Request) { w.Header().Set("X-Reached", "yes") }
		}
	}
	tm.srv = httptest.NewServer(securityHeaders(a.serve(rs)))
	t.Cleanup(tm.srv.Close)
	return tm
}

// send makes a request as whoever token belongs to ("" for nobody).
func (tm *team) send(t *testing.T, token, method, path, body string) (*http.Response, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, tm.srv.URL+path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("X-Pier", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	json.Unmarshal(raw, &out)
	if out == nil {
		out = map[string]any{"raw": string(raw)}
	}
	return resp, out
}

var examplePath = strings.NewReplacer(
	"{project}", "demo", "{id}", "1", "{host}", "demo.example.com", "{name}", "API_KEY",
	"{code}", "ABCD-EFGH", "{answer}", "deny", "{kind}", "cache", "{run}", "1",
)

// TestEveryRouteHasARoleDecision walks every route pierd serves and checks
// what the guard lets a viewer, a member and the owner do with it, against
// the decisions above. A new route fails here until someone decides.
func TestEveryRouteHasARoleDecision(t *testing.T) {
	tm := newTeam(t, true)
	seen := map[string]bool{}
	for _, rt := range tm.api.routes() {
		if seen[rt.pattern] {
			t.Errorf("%s is listed twice", rt.pattern)
		}
		seen[rt.pattern] = true
		want, ok := decisions[rt.pattern]
		if !ok {
			t.Errorf("%s has no decision in the test: who may use it?", rt.pattern)
			continue
		}
		if got := accessNames[rt.access]; got != want {
			t.Errorf("%s is open to %q, but the decision is %q", rt.pattern, got, want)
			continue
		}
		if (rt.access == member || rt.access == owner) && rt.does == "" {
			t.Errorf("%s doesn't say what it does, for its refusal", rt.pattern)
		}
		method, path, _ := strings.Cut(rt.pattern, " ")
		path = examplePath.Replace(path)
		reached := func(token string) (bool, *http.Response, map[string]any) {
			resp, body := tm.send(t, token, method, path, "{}")
			return resp.Header.Get("X-Reached") == "yes", resp, body
		}
		if rt.access == public {
			if ok, resp, _ := reached(""); !ok {
				t.Errorf("%s: nobody signed in got %d, want it public", rt.pattern, resp.StatusCode)
			}
			continue
		}
		if ok, resp, _ := reached(""); ok || resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: nobody signed in got %d, want 401", rt.pattern, resp.StatusCode)
		}
		for who, token := range map[string]string{"viewer": tm.viewer, "member": tm.member, "owner": tm.owner} {
			allowed := rank[who] >= rt.access
			ok, resp, body := reached(token)
			switch {
			case allowed && !ok:
				t.Errorf("%s: the %s was refused (%d %v), but may use it", rt.pattern, who, resp.StatusCode, body)
			case !allowed && ok:
				t.Errorf("%s: the %s got through, but may not use it", rt.pattern, who)
			case !allowed && (resp.StatusCode != http.StatusForbidden || body["need"] == nil || body["role"] != who):
				t.Errorf("%s: the %s got %d %v, want 403 naming the role needed", rt.pattern, who, resp.StatusCode, body)
			}
		}
	}
	for pattern := range decisions {
		if !seen[pattern] {
			t.Errorf("the test decides on %s, which isn't a route any more", pattern)
		}
	}
	// What isn't a route is no route at all, for anyone.
	if resp, _ := tm.send(t, tm.owner, "POST", "/api/projects/demo/secret-door", "{}"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("an unknown endpoint answered %d, want 404", resp.StatusCode)
	}
}

func TestUndecidedRouteIsRefused(t *testing.T) {
	a := &API{}
	h := a.guard(route{pattern: "GET /api/new", handler: func(w http.ResponseWriter, r *http.Request) { w.Header().Set("X-Reached", "yes") }})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/new", nil))
	if rec.Code != http.StatusForbidden || rec.Header().Get("X-Reached") != "" {
		t.Fatalf("an undecided route answered %d", rec.Code)
	}
}

func TestRefusalsAreOneSentence(t *testing.T) {
	tm := newTeam(t, false)
	_, body := tm.send(t, tm.viewer, "POST", "/api/projects/blog/deployments?target=preview", "")
	if body["error"] != "Viewers can't deploy. Ask the owner to make you a member." {
		t.Errorf("deploy as a viewer said %v", body)
	}
	_, body = tm.send(t, tm.viewer, "POST", "/api/projects/blog/promote", `{"id":"abc123"}`)
	if body["error"] != "Viewers can't promote deployments. Ask the owner to make you a member." {
		t.Errorf("promote as a viewer said %v", body)
	}
	_, body = tm.send(t, tm.viewer, "POST", "/api/projects/blog/rollback", `{}`)
	if body["error"] != "Viewers can't roll back production. Ask the owner to make you a member." {
		t.Errorf("rollback as a viewer said %v", body)
	}
	_, body = tm.send(t, tm.member, "POST", "/api/invites", `{"role":"member"}`)
	if body["error"] != "Only the owner can invite people." {
		t.Errorf("an invite by a member said %v", body)
	}
}

// A role is read with every request, so changing it, or removing someone,
// applies to the very next one, from any browser or computer.
func TestRoleChangesAndRemovalApplyAtOnce(t *testing.T) {
	tm := newTeam(t, false)
	ctx := context.Background()
	if resp, body := tm.send(t, tm.member, "POST", "/api/projects", `{"name":"blog"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("a member creating a project got %d %v", resp.StatusCode, body)
	}
	memberPath := fmt.Sprintf("/api/members/%d", tm.ids["member"])
	if resp, body := tm.send(t, tm.owner, "PUT", memberPath, `{"role":"viewer"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("making the member a viewer got %d %v", resp.StatusCode, body)
	}
	resp, body := tm.send(t, tm.member, "POST", "/api/projects", `{"name":"blog-2"}`)
	if resp.StatusCode != http.StatusForbidden || body["error"] != "Viewers can't create projects. Ask the owner to make you a member." {
		t.Fatalf("after becoming a viewer, creating a project got %d %v", resp.StatusCode, body)
	}
	if resp, _ := tm.send(t, tm.member, "GET", "/api/projects", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("a viewer looking at projects got %d", resp.StatusCode)
	}
	if resp, _ := tm.send(t, tm.owner, "DELETE", memberPath, ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("removing them got %d", resp.StatusCode)
	}
	if resp, _ := tm.send(t, tm.member, "GET", "/api/projects", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("after removal their token got %d, want 401", resp.StatusCode)
	}
	if left, err := tm.st.Sessions(ctx, tm.ids["member"]); err != nil || len(left) != 0 {
		t.Fatalf("after removal %d sessions are left (%v)", len(left), err)
	}
	if _, err := tm.st.UserByEmail(ctx, "member@example.com"); err != store.ErrNotFound {
		t.Fatalf("after removal the account is still there: %v", err)
	}
	// The activity says who did each thing.
	events, _ := tm.st.Events(ctx, "", 0)
	var removed bool
	for _, e := range events {
		if e.Title == "Removed someone" && e.Actor == "Owner" {
			removed = true
		}
	}
	if !removed {
		t.Errorf("activity doesn't say the owner removed them: %+v", events)
	}
}

func TestOwnerStaysOwner(t *testing.T) {
	tm := newTeam(t, false)
	ownerPath := fmt.Sprintf("/api/members/%d", tm.ids["owner"])
	if resp, _ := tm.send(t, tm.owner, "PUT", ownerPath, `{"role":"viewer"}`); resp.StatusCode != http.StatusConflict {
		t.Errorf("demoting the owner got %d, want 409", resp.StatusCode)
	}
	if resp, _ := tm.send(t, tm.owner, "DELETE", ownerPath, ""); resp.StatusCode != http.StatusConflict {
		t.Errorf("removing the owner got %d, want 409", resp.StatusCode)
	}
	viewerPath := fmt.Sprintf("/api/members/%d", tm.ids["viewer"])
	if resp, _ := tm.send(t, tm.owner, "PUT", viewerPath, `{"role":"owner"}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("making a second owner got %d, want 400", resp.StatusCode)
	}
	if resp, _ := tm.send(t, tm.member, "DELETE", viewerPath, ""); resp.StatusCode != http.StatusForbidden {
		t.Errorf("a member removing someone got %d, want 403", resp.StatusCode)
	}
	if o, _ := tm.st.Owner(context.Background()); o.ID != tm.ids["owner"] || o.Role != store.RoleOwner {
		t.Errorf("the owner is now %+v", o)
	}
}

// Viewers see variables by name. Their values never reach a viewer: not by
// revealing one, and not in a log a build or an app printed them into.
func TestViewersNeverSeeValues(t *testing.T) {
	tm := newTeam(t, false)
	ctx := context.Background()
	if err := tm.st.CreateProject(ctx, store.Project{Name: "blog"}); err != nil {
		t.Fatal(err)
	}
	const secret = "sk_live_9f8e7d6c5b"
	if resp, body := tm.send(t, tm.member, "PUT", "/api/projects/blog/variables", `{"name":"API_KEY","value":"`+secret+`"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("setting a variable got %d %v", resp.StatusCode, body)
	}
	if err := tm.st.CreateDeployment(ctx, store.Deployment{ID: "c19e77", Project: "blog", Branch: "main", Status: store.Ready, Target: store.TargetProduction}); err != nil {
		t.Fatal(err)
	}
	log := "12\tpier    received main, for production\n40\tbuild   the key is " + secret + "\n"
	if err := os.WriteFile(filepath.Join(tm.api.Deployer.Logs, "c19e77.log"), []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
	read := func(token string) string {
		resp, body := tm.send(t, token, "GET", "/api/projects/blog/deployments/c19e77/log", "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("reading the log got %d %v", resp.StatusCode, body)
		}
		raw, _ := json.Marshal(body)
		return string(raw)
	}
	if got := read(tm.viewer); strings.Contains(got, secret) || !strings.Contains(got, hiddenValue) || !strings.Contains(got, "for production") {
		t.Errorf("a viewer read the log as %s", got)
	}
	if got := read(tm.member); !strings.Contains(got, secret) {
		t.Errorf("a member read the log as %s", got)
	}
	resp, body := tm.send(t, tm.viewer, "POST", "/api/projects/blog/variables/reveal", `{"name":"API_KEY","scopes":["production"]}`)
	if resp.StatusCode != http.StatusForbidden || body["error"] != "Viewers can't see variable values. Ask the owner to make you a member." {
		t.Errorf("a viewer revealing a value got %d %v", resp.StatusCode, body)
	}
	resp, body = tm.send(t, tm.viewer, "GET", "/api/projects/blog/variables", "")
	if raw, _ := json.Marshal(body); resp.StatusCode != http.StatusOK || strings.Contains(string(raw), secret) || !strings.Contains(string(raw), "API_KEY") {
		t.Errorf("a viewer listing variables got %d %s", resp.StatusCode, raw)
	}
	if resp, body := tm.send(t, tm.member, "POST", "/api/projects/blog/variables/reveal", `{"name":"API_KEY","scopes":["production"]}`); body["value"] != secret {
		t.Errorf("a member revealing the value got %d %v", resp.StatusCode, body)
	}
}

func TestInviteMakesOneAccount(t *testing.T) {
	tm := newTeam(t, false)
	req, _ := http.NewRequest("POST", tm.srv.URL+"/api/invites", strings.NewReader(`{"role":"viewer"}`))
	req.Header.Set("Authorization", "Bearer "+tm.owner)
	req.Header.Set("Origin", "http://pier.localhost:7777")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var made struct {
		Link   string
		Invite store.Invite
	}
	json.NewDecoder(resp.Body).Decode(&made)
	resp.Body.Close()
	link, token, _ := strings.Cut(made.Link, "#")
	if resp.StatusCode != http.StatusCreated || link != "http://pier.localhost:7777/invite" || !strings.HasPrefix(token, invitePrefix) {
		t.Fatalf("making an invite got %d %+v", resp.StatusCode, made)
	}
	if _, body := tm.send(t, tm.owner, "GET", "/api/invites", ""); len(body["invites"].([]any)) != 1 {
		t.Fatalf("waiting invites: %v", body)
	}

	lookup := `{"token":"` + token + `"}`
	if resp, body := tm.send(t, "", "POST", "/api/invites/lookup", lookup); resp.StatusCode != http.StatusOK || body["role"] != "viewer" || body["invited_by"] != "Owner" {
		t.Fatalf("looking the invite up got %d %v", resp.StatusCode, body)
	}
	accept := func(email, password string) (*http.Response, map[string]any) {
		return tm.send(t, "", "POST", "/api/invites/accept",
			`{"token":"`+token+`","name":"Ada","email":"`+email+`","password":"`+password+`"}`)
	}
	if resp, body := accept("ada@example.com", "short"); resp.StatusCode != http.StatusBadRequest || body["field"] != "password" {
		t.Fatalf("a short password got %d %v", resp.StatusCode, body)
	}
	if resp, body := accept("owner@example.com", "a long enough password"); resp.StatusCode != http.StatusConflict || body["field"] != "email" {
		t.Fatalf("a taken email got %d %v", resp.StatusCode, body)
	}
	resp, body := accept("ada@example.com", "a long enough password")
	user, _ := body["user"].(map[string]any)
	if resp.StatusCode != http.StatusOK || user["role"] != "viewer" || user["name"] != "Ada" {
		t.Fatalf("accepting got %d %v", resp.StatusCode, body)
	}
	var cookie string
	for _, c := range resp.Cookies() {
		if c.Name == cookieName {
			cookie = c.Value
		}
	}
	if cookie == "" {
		t.Fatal("accepting didn't sign them in")
	}
	// Signed in with that cookie, they look but can't change.
	get, _ := http.NewRequest("GET", tm.srv.URL+"/api/session", nil)
	get.AddCookie(&http.Cookie{Name: cookieName, Value: cookie})
	if r, err := http.DefaultClient.Do(get); err != nil || r.StatusCode != http.StatusOK {
		t.Fatalf("their session: %v %v", r, err)
	}
	// The invite works once.
	if resp, _ := accept("grace@example.com", "a long enough password"); resp.StatusCode != http.StatusGone {
		t.Fatalf("using the invite again got %d, want 410", resp.StatusCode)
	}
	if resp, _ := tm.send(t, "", "POST", "/api/invites/lookup", lookup); resp.StatusCode != http.StatusGone {
		t.Fatalf("looking a used invite up got %d, want 410", resp.StatusCode)
	}
	if _, body := tm.send(t, tm.owner, "GET", "/api/invites", ""); len(body["invites"].([]any)) != 0 {
		t.Fatalf("a used invite still waits: %v", body)
	}
	// A revoked one doesn't work at all.
	_, body = tm.send(t, tm.owner, "POST", "/api/invites", `{"role":"member"}`)
	_, second, _ := strings.Cut(body["link"].(string), "#")
	id := int64(body["invite"].(map[string]any)["id"].(float64))
	if resp, _ := tm.send(t, tm.owner, "DELETE", fmt.Sprintf("/api/invites/%d", id), ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("revoking got %d", resp.StatusCode)
	}
	if resp, _ := tm.send(t, "", "POST", "/api/invites/lookup", `{"token":"`+second+`"}`); resp.StatusCode != http.StatusGone {
		t.Fatalf("a revoked invite got %d, want 410", resp.StatusCode)
	}
	// And the new account signs in with its password, as a viewer.
	_, body = tm.send(t, "", "POST", "/api/session", `{"email":"ada@example.com","password":"a long enough password"}`)
	if user, _ := body["user"].(map[string]any); user["role"] != "viewer" {
		t.Fatalf("signing in as Ada: %v", body)
	}
}

// A computer approved by someone acts as them: a viewer's computer can look,
// and is told plainly that it can't deploy.
func TestComputerActsAsWhoApprovedIt(t *testing.T) {
	tm := newTeam(t, false)
	_, start := tm.send(t, "", "POST", "/api/cli/login", `{"name":"laptop"}`)
	code, _ := start["code"].(string)
	if resp, body := tm.send(t, tm.viewer, "POST", "/api/cli/login/"+code+"/approve", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("a viewer approving their computer got %d %v", resp.StatusCode, body)
	}
	_, poll := tm.send(t, "", "POST", "/api/cli/login/poll", `{"poll":"`+start["poll"].(string)+`"}`)
	token, _ := poll["token"].(string)
	if poll["email"] != "viewer@example.com" || token == "" {
		t.Fatalf("polling got %v", poll)
	}
	if resp, _ := tm.send(t, token, "GET", "/api/projects", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("the computer looking got %d", resp.StatusCode)
	}
	_, body := tm.send(t, token, "POST", "/api/projects/blog/deployments?target=production", "")
	if body["error"] != "Viewers can't deploy. Ask the owner to make you a member." {
		t.Fatalf("the computer deploying got %v", body)
	}
}

// A viewer reads a project's jobs and their runs, but never a variable's
// value or the CRON_SECRET a path job sends, not in a run's output and not
// in why it failed; and can't add or run a job.
func TestViewersReadJobsWithoutSecrets(t *testing.T) {
	tm := newTeam(t, false)
	ctx := context.Background()
	st, d := tm.st, tm.api.Deployer
	tm.api.Jobs = &jobs.Scheduler{Store: st, Deployer: d, Log: tm.api.Log, Logs: t.TempDir()}
	st.CreateProject(ctx, store.Project{Name: "blog"})
	dep := store.Deployment{ID: "c19e77", Project: "blog", Branch: "main", Target: store.TargetProduction, Status: store.Ready}
	st.CreateDeployment(ctx, dep)
	st.SetAlias(ctx, store.Alias{Host: "blog.pier.localhost", Project: "blog", Kind: store.KindProduction, Branch: "main", DeploymentID: dep.ID})
	cron, _, err := d.CronSecret(ctx, "blog")
	if err != nil || len(cron) < 6 {
		t.Fatalf("cron secret %q, %v", cron, err)
	}
	const value = "sk_live_9f8e7d6c5b"
	tm.send(t, tm.member, "PUT", "/api/projects/blog/variables", `{"name":"API_KEY","value":"`+value+`"}`)
	if resp, body := tm.send(t, tm.member, "POST", "/api/projects/blog/jobs", `{"name":"nightly","kind":"path","schedule":"0 3 * * *","path":"/api/cron"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("a member adding a job got %d %v", resp.StatusCode, body)
	}
	run, err := st.StartRun(ctx, store.JobRun{Job: 1, Project: "blog", StartedBy: "manual", Status: store.RunFailed, StartedAt: time.Now(),
		Detail: "It answered 401 to Bearer " + cron})
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(tm.api.Jobs.Logs, "blog"), 0o755)
	output := fmt.Sprintf("5\tstdout\tauthorization: Bearer %s\n9\tstdout\tkey=%s\n", cron, value)
	os.WriteFile(filepath.Join(tm.api.Jobs.Logs, "blog", fmt.Sprint(run)+".log"), []byte(output), 0o644)

	for _, path := range []string{"/api/projects/blog/jobs", "/api/projects/blog/jobs/1", "/api/projects/blog/jobs/1/runs", fmt.Sprintf("/api/projects/blog/jobs/1/runs/%d", run)} {
		resp, body := tm.send(t, tm.viewer, "GET", path, "")
		raw, _ := json.Marshal(body)
		if resp.StatusCode != http.StatusOK || strings.Contains(string(raw), cron) || strings.Contains(string(raw), value) {
			t.Errorf("a viewer reading %s got %d %s", path, resp.StatusCode, raw)
		}
	}
	_, body := tm.send(t, tm.viewer, "GET", fmt.Sprintf("/api/projects/blog/jobs/1/runs/%d", run), "")
	if raw, _ := json.Marshal(body); strings.Count(string(raw), hiddenValue) != 3 {
		t.Errorf("a viewer's run should show three hidden values: %s", raw)
	}
	_, body = tm.send(t, tm.member, "GET", fmt.Sprintf("/api/projects/blog/jobs/1/runs/%d", run), "")
	if raw, _ := json.Marshal(body); !strings.Contains(string(raw), cron) {
		t.Errorf("a member reads the run as %s", raw)
	}
	if _, body := tm.send(t, tm.viewer, "POST", "/api/projects/blog/jobs/1/run", ""); body["error"] != "Viewers can't run jobs. Ask the owner to make you a member." {
		t.Errorf("a viewer running a job got %v", body)
	}
	if _, body := tm.send(t, tm.viewer, "POST", "/api/projects/blog/jobs", `{}`); body["error"] != "Viewers can't add jobs. Ask the owner to make you a member." {
		t.Errorf("a viewer adding a job got %v", body)
	}
}

// With email set up in Alerts, an invite can be emailed, from the
// configured sender; the link comes back to copy either way, and a failed
// send says why.
func TestInviteByEmail(t *testing.T) {
	tm := newTeam(t, false)
	var mu sync.Mutex
	var sent []map[string]any
	fail := false
	resend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		defer mu.Unlock()
		if fail {
			w.WriteHeader(http.StatusUnprocessableEntity)
			io.WriteString(w, `{"message":"The from address isn't verified."}`)
			return
		}
		sent = append(sent, body)
	}))
	t.Cleanup(resend.Close)
	tm.api.Alerts = &alerts.Notifier{Store: tm.st, Box: tm.api.Deployer.Secrets, Log: tm.api.Log, Server: "pier.localhost", ResendURL: resend.URL, Retries: []time.Duration{}}

	if _, body := tm.send(t, tm.owner, "GET", "/api/invites", ""); body["email"] != false {
		t.Fatalf("before email is set up: %v", body)
	}
	if resp, body := tm.send(t, tm.owner, "POST", "/api/invites", `{"role":"member","email":"ada@example.com"}`); resp.StatusCode != http.StatusBadRequest || body["field"] != "email" {
		t.Fatalf("emailing without email set up: %d %v", resp.StatusCode, body)
	}
	if resp, body := tm.send(t, tm.owner, "PUT", "/api/alerts/email", `{"provider":"resend","api_key":"re_test","from":"Pier <pier@example.com>"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("setting email up: %d %v", resp.StatusCode, body)
	}
	if _, body := tm.send(t, tm.owner, "GET", "/api/invites", ""); body["email"] != true {
		t.Fatalf("after email is set up: %v", body)
	}
	resp, body := tm.send(t, tm.owner, "POST", "/api/invites", `{"role":"member","email":"ada@example.com"}`)
	link, _ := body["link"].(string)
	if resp.StatusCode != http.StatusCreated || body["emailed"] != "ada@example.com" || !strings.Contains(link, "/invite#pinv_") {
		t.Fatalf("an emailed invite: %d %v", resp.StatusCode, body)
	}
	mu.Lock()
	if len(sent) != 1 || sent[0]["from"] != "Pier <pier@example.com>" || fmt.Sprint(sent[0]["to"]) != "[ada@example.com]" ||
		!strings.Contains(fmt.Sprint(sent[0]["subject"]), "Owner invited you to Pier") || !strings.Contains(fmt.Sprint(sent[0]["text"]), link) ||
		!strings.Contains(fmt.Sprint(sent[0]["text"]), "as a member") {
		t.Errorf("the invite email: %+v", sent)
	}
	fail = true
	mu.Unlock()
	resp, body = tm.send(t, tm.owner, "POST", "/api/invites", `{"role":"viewer","email":"grace@example.com"}`)
	if resp.StatusCode != http.StatusCreated || body["link"] == nil || !strings.Contains(fmt.Sprint(body["email_error"]), "verified") {
		t.Errorf("a failed send: %d %v", resp.StatusCode, body)
	}
}

// Alert emails go to the owner and members who have them on. A viewer has
// no switch: an alert can quote a value.
func TestViewersGetNoAlertEmails(t *testing.T) {
	tm := newTeam(t, false)
	if _, body := tm.send(t, tm.member, "GET", "/api/account/alerts", ""); body["email"] != true {
		t.Errorf("a member's alert emails: %v", body)
	}
	if _, body := tm.send(t, tm.viewer, "GET", "/api/account/alerts", ""); body["email"] != false {
		t.Errorf("a viewer's alert emails: %v", body)
	}
	if resp, _ := tm.send(t, tm.viewer, "PUT", "/api/account/alerts", `{"email":true}`); resp.StatusCode != http.StatusForbidden {
		t.Errorf("a viewer turning alert emails on got %d", resp.StatusCode)
	}
	people, err := tm.st.AlertRecipients(context.Background())
	if err != nil || len(people) != 2 || people[0].Email != "owner@example.com" || people[1].Email != "member@example.com" {
		t.Errorf("alert emails go to %+v (%v)", people, err)
	}
}
