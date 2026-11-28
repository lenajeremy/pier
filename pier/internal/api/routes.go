package api

import (
	"io"
	"net/http"

	"pier/internal/store"
)

// access is who may use a route. Every route in routes says, and the guard
// enforces it before the handler runs, so no handler checks a role itself.
type access int

const (
	// undecided is a route nobody chose for: it's refused to everyone, so a
	// forgotten decision can't open anything.
	undecided access = iota
	// public needs no account: signing in, joining with an invite, and what
	// GitHub sends, which it signs. Each handler checks what it needs.
	public
	// viewer is anyone signed in: looking at everything but variable values,
	// and looking after their own account.
	viewer
	// member deploys, promotes, rolls back, restarts and changes every project.
	member
	// owner is the owner alone: members, the server's own settings and the
	// GitHub App.
	owner
)

// rank orders the roles, so a role can do what any role below it can.
var rank = map[string]access{store.RoleViewer: viewer, store.RoleMember: member, store.RoleOwner: owner}

func (need access) allows(role string) bool {
	return need >= viewer && rank[role] >= need
}

// route is one endpoint, who may use it, and what it does, in words that
// finish "Viewers can't …" or "Only the owner can …".
type route struct {
	pattern string
	access  access
	does    string
	handler http.HandlerFunc
}

// routes is every endpoint pierd serves, with who may use each. A new
// endpoint goes here, with a decision; TestEveryRouteHasARoleDecision fails
// until the test knows what viewers and members may do with it.
func (a *API) routes() []route {
	return []route{
		// Before there's a session: signing in, pier login asking for
		// approval, joining with an invite, and GitHub's signed deliveries.
		{"GET /healthz", public, "", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok\n") }},
		{"POST /api/session", public, "", a.signIn},
		{"POST /api/cli/login", public, "", a.cliStart},
		{"POST /api/cli/login/poll", public, "", a.cliPoll},
		{"POST /api/invites/lookup", public, "", a.lookupInvite},
		{"POST /api/invites/accept", public, "", a.acceptInvite},
		{"POST /github/events", public, "", a.ghEvent},

		// Your own account. Everyone signed in can see and end their own
		// sessions, change their own password, and approve their own
		// computers, which then act with their role.
		{"GET /api/session", viewer, "", a.session},
		{"DELETE /api/session", viewer, "", a.signOut},
		{"GET /api/sessions", viewer, "", a.sessions},
		{"DELETE /api/sessions/others", viewer, "", a.signOutOthers},
		{"DELETE /api/sessions/{id}", viewer, "", a.signOutSession},
		{"POST /api/settings/password", viewer, "", a.password},
		// Whether you get alert emails. A viewer never does, since an alert
		// quotes a build's or a job's output as it was; the handler says so.
		{"GET /api/account/alerts", viewer, "", a.accountAlerts},
		{"PUT /api/account/alerts", viewer, "", a.accountAlerts},
		{"GET /api/cli/login/{code}", viewer, "", a.cliRequest},
		{"POST /api/cli/login/{code}/{answer}", viewer, "", a.cliAnswer},

		// Looking, for everyone signed in. Variables come by name only;
		// logs hide their values from viewers.
		{"GET /api/version", viewer, "", a.versionInfo},
		{"GET /api/board", viewer, "", a.board},
		{"GET /api/projects", viewer, "", a.projects},
		{"GET /api/projects/{project}", viewer, "", a.project},
		{"GET /api/projects/{project}/favicon", viewer, "", a.favicon},
		{"GET /api/projects/{project}/settings", viewer, "", a.settings},
		{"GET /api/projects/{project}/folders", viewer, "", a.projectFolders},
		{"GET /api/projects/{project}/deployments", viewer, "", a.deployments},
		{"GET /api/projects/{project}/deployments/{id}", viewer, "", a.deployment},
		{"GET /api/projects/{project}/deployments/{id}/log", viewer, "", a.deploymentLog},
		{"GET /api/projects/{project}/deployments/{id}/runtime-log", viewer, "", a.deploymentRuntimeLog},
		{"GET /api/projects/{project}/variables", viewer, "", a.variables},
		{"GET /api/projects/{project}/requests", viewer, "", a.requestLines},
		{"GET /api/projects/{project}/requests/chart", viewer, "", a.requestChart},
		{"GET /api/projects/{project}/domains", viewer, "", a.domains},
		{"GET /api/projects/{project}/database", viewer, "", a.getDatabase},
		{"GET /api/projects/{project}/storage", viewer, "", a.getStorage},
		{"GET /api/cloudflare", viewer, "", a.getCloudflare},
		{"GET /api/projects/{project}/jobs", viewer, "", a.listJobs},
		{"GET /api/projects/{project}/jobs/{id}", viewer, "", a.getJob},
		{"GET /api/projects/{project}/jobs/{id}/runs", viewer, "", a.jobRuns},
		// A run's output, like a runtime log: values hidden from viewers.
		{"GET /api/projects/{project}/jobs/{id}/runs/{run}", viewer, "", a.jobRun},
		// Only says when a schedule runs, in words.
		{"POST /api/jobs/schedule", viewer, "", a.describeSchedule},
		{"GET /api/shared-variables", viewer, "", a.shared},
		{"GET /api/activity", viewer, "", a.activity},
		{"GET /api/members", viewer, "", a.members},
		{"GET /api/server", viewer, "", a.health},
		{"GET /api/server/history", viewer, "", a.history},
		{"GET /api/settings", viewer, "", a.serverSettings},
		// Checking the server's DNS only looks the addresses up.
		{"POST /api/settings/dns", viewer, "", a.checkAddresses},
		{"GET /api/github", viewer, "", a.ghInfo},
		{"GET /api/github/activity", viewer, "", a.ghActivity},
		{"GET /api/github/repositories", viewer, "", a.ghList},
		{"GET /api/github/repositories/{id}/folders", viewer, "", a.repoFolders},

		// Deploying and changing projects, for members and the owner.
		{"POST /api/projects", member, "create projects", a.createProject},
		{"DELETE /api/projects/{project}", member, "delete projects", a.deleteProject},
		{"PUT /api/projects/{project}/settings", member, "change a project's settings", a.saveSettings},
		{"PUT /api/projects/{project}/address", member, "change a project's address", a.setAddress},
		{"POST /api/projects/{project}/deployments", member, "deploy", a.upload},
		{"POST /api/projects/{project}/redeploy", member, "redeploy", a.redeploy},
		{"POST /api/projects/{project}/promote", member, "promote deployments", a.promote},
		{"POST /api/projects/{project}/rollback", member, "roll back production", a.rollback},
		{"POST /api/projects/{project}/rollback/undo", member, "undo a rollback", a.undoRollback},
		{"POST /api/projects/{project}/deployments/{id}/restart", member, "restart a deployment", a.deploymentRestart},
		{"PUT /api/projects/{project}/variables", member, "change variables", a.setVariable},
		{"POST /api/projects/{project}/variables/import", member, "import variables", a.importVariables},
		{"POST /api/projects/{project}/variables/delete", member, "delete variables", a.deleteVariable},
		{"POST /api/projects/{project}/variables/reveal", member, "see variable values", a.revealVariable},
		{"POST /api/projects/{project}/domains", member, "add domains", a.addDomain},
		{"POST /api/projects/{project}/domains/{host}/check", member, "check domains", a.checkDomain},
		{"DELETE /api/projects/{project}/domains/{host}", member, "remove domains", a.removeDomain},
		{"GET /api/projects/{project}/database/url", member, "see a database's password", a.databaseURL},
		{"GET /api/projects/{project}/database/tables", member, "browse a database's data", a.databaseTables},
		{"GET /api/projects/{project}/database/tables/{schema}/{table}", member, "browse a database's data", a.databaseRows},
		{"POST /api/projects/{project}/database/query", member, "run queries", a.runQuery},
		{"GET /api/projects/{project}/database/tunnel", member, "connect to databases", a.databaseTunnel},
		{"POST /api/projects/{project}/database", member, "add databases", a.createDatabase},
		{"PUT /api/projects/{project}/database", member, "change databases", a.changeDatabase},
		{"DELETE /api/projects/{project}/database", member, "delete databases", a.deleteDatabase},
		{"POST /api/projects/{project}/database/restart", member, "restart databases", a.restartDatabase},
		{"POST /api/projects/{project}/database/backups", member, "back up databases", a.backUpDatabase},
		{"GET /api/projects/{project}/database/backups/{id}/file", member, "download backups", a.downloadBackup},
		{"POST /api/projects/{project}/database/backups/{id}/restore", member, "restore backups", a.restoreBackup},
		{"DELETE /api/projects/{project}/database/backups/{id}", member, "delete backups", a.deleteBackup},
		{"POST /api/projects/{project}/database/databases", member, "add databases", a.addProjectDatabase},
		{"PUT /api/projects/{project}/database/databases/{name}", member, "change databases", a.linkProjectDatabase},
		{"DELETE /api/projects/{project}/database/databases/{name}", member, "delete databases", a.dropProjectDatabase},
		{"POST /api/projects/{project}/database/databases/{name}/externals", member, "make external URLs", a.createExternal},
		{"GET /api/projects/{project}/database/externals/{role}", member, "see a database's password", a.externalURL},
		{"DELETE /api/projects/{project}/database/externals/{role}", member, "revoke external URLs", a.revokeExternal},
		{"GET /api/projects/{project}/storage/{bucket}/variables", member, "see a bucket's keys", a.bucketVariables},
		{"GET /api/projects/{project}/storage/{bucket}/files", member, "browse files", a.listFiles},
		{"GET /api/projects/{project}/storage/{bucket}/file", member, "browse files", a.getFile},
		{"PUT /api/projects/{project}/storage/{bucket}/file", member, "upload files", a.putFile},
		{"DELETE /api/projects/{project}/storage/{bucket}/files", member, "delete files", a.deleteFiles},
		{"POST /api/projects/{project}/storage", member, "add buckets", a.addBucket},
		{"PUT /api/projects/{project}/storage/{bucket}", member, "change buckets", a.changeBucket},
		{"DELETE /api/projects/{project}/storage/{bucket}", member, "delete buckets", a.deleteBucket},
		{"POST /api/projects/{project}/jobs", member, "add jobs", a.saveJob},
		{"PUT /api/projects/{project}/jobs/{id}", member, "change jobs", a.saveJob},
		{"DELETE /api/projects/{project}/jobs/{id}", member, "delete jobs", a.deleteJob},
		{"POST /api/projects/{project}/jobs/{id}/run", member, "run jobs", a.runJob},
		// Shared variables are project variables kept in one place: whoever
		// can set a project's variables can set them.
		{"POST /api/shared-variables", member, "add shared variables", a.shared},
		{"PUT /api/shared-variables/{name}", member, "change shared variables", a.shared},
		{"DELETE /api/shared-variables/{name}", member, "delete shared variables", a.shared},
		// Approving a fork's build is deploying it.
		{"POST /api/github/import", member, "import repositories", a.ghImport},
		{"POST /api/github/jobs/{id}/resume", member, "start builds", a.ghResume},
		{"POST /api/github/jobs/{id}/dismiss", member, "dismiss builds", a.ghDismiss},

		// The server itself, for the owner alone.
		{"PUT /api/settings", owner, "change the server's addresses", a.serverSettings},
		{"POST /api/server/cleanup/{kind}", owner, "clean up the server", a.cleanup},
		{"GET /api/server/upgrade", owner, "see server updates", a.upgradeInfo},
		{"POST /api/server/upgrade", owner, "update the server", a.requestUpgrade},
		{"POST /api/github/manifest", owner, "connect a GitHub App", a.ghManifest},
		{"GET /github/callback", owner, "connect a GitHub App", a.ghCallback},
		{"DELETE /api/github", owner, "disconnect the GitHub App", a.ghDisconnect},
		{"PUT /api/cloudflare", owner, "connect Cloudflare", a.connectCloudflare},
		{"DELETE /api/cloudflare", owner, "disconnect Cloudflare", a.disconnectCloudflare},
		{"GET /api/alerts", owner, "see alert settings", a.alertSettings},
		{"PUT /api/alerts", owner, "change alert settings", a.setAlertEvents},
		{"PUT /api/alerts/{kind}", owner, "set up alerts", a.setAlertChannel},
		{"DELETE /api/alerts/{kind}", owner, "stop alerts", a.deleteAlertChannel},
		{"POST /api/alerts/{kind}/test", owner, "send test alerts", a.testAlertChannel},
		{"POST /api/alerts/webhook/secret", owner, "replace the webhook's signing secret", a.newWebhookSecret},
		{"PUT /api/members/{id}", owner, "change someone's role", a.setRole},
		{"DELETE /api/members/{id}", owner, "remove people", a.removeMember},
		{"GET /api/invites", owner, "see invites", a.invites},
		{"POST /api/invites", owner, "invite people", a.createInvite},
		{"DELETE /api/invites/{id}", owner, "revoke invites", a.revokeInvite},
	}
}

// serve builds the mux from routes, each behind its guard. Anything else
// under /api/ is answered 404, and the rest is the dashboard.
func (a *API) serve(rs []route) *http.ServeMux {
	mux := http.NewServeMux()
	for _, rt := range rs {
		mux.Handle(rt.pattern, a.guard(rt))
	}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such API endpoint"})
	})
	if a.Web != nil {
		mux.Handle("/", a.Web)
	}
	return mux
}

// guard lets a request through to a route only when the role of whoever
// sent it allows. The role is read with the session on every request, so a
// change of role, or a removal, applies at once.
func (a *API) guard(rt route) http.Handler {
	switch rt.access {
	case public:
		return rt.handler
	case viewer, member, owner:
		return a.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role := userFrom(r.Context()).Role
			if !rt.access.allows(role) {
				refuse(w, rt, role)
				return
			}
			rt.handler(w, r)
		}))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Nobody can use this yet."})
	})
}

// refuse says, in one sentence, why a role can't do something and who can
// change that. It also names the role needed, for the CLI.
func refuse(w http.ResponseWriter, rt route, role string) {
	msg := "Only the owner can " + rt.does + "."
	need := store.RoleOwner
	if rt.access == member {
		msg = "Viewers can't " + rt.does + ". Ask the owner to make you a member."
		need = store.RoleMember
	}
	writeJSON(w, http.StatusForbidden, map[string]string{"error": msg, "role": role, "need": need})
}
