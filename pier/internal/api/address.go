package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"pier/internal/names"
	"pier/internal/store"
)

// deploymentShaped matches a name like blog-c19e77, which is how a
// deployment's own address starts.
var deploymentShaped = regexp.MustCompile(`^(.+)-[0-9a-f]{6}$`)

// setAddress renames the address a project's production has under the base
// domain, so google-prep-os.example.com can become prep.example.com. Only
// production's address moves: previews and each deployment's own address
// keep the project's name, so links already shared keep working. The old
// address stops serving at once. An empty subdomain goes back to the
// project's name.
func (a *API) setAddress(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	p, err := a.Store.Project(r.Context(), project)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "No project called " + project + "."})
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	var req struct {
		Subdomain string `json:"subdomain"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Send the address's new name.", "field": "subdomain"})
		return
	}
	sub := strings.ToLower(strings.TrimSpace(req.Subdomain))
	if sub == project {
		sub = ""
	}
	label := sub
	if label == "" {
		label = project
	}
	bad := func(msg string) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": msg, "field": "subdomain"})
	}
	switch {
	case !names.ValidProject(label):
		bad("Use lowercase letters, numbers and single dashes, like my-site.")
		return
	case a.reserved(label):
		bad(label + " is the dashboard's own address. Choose another name.")
		return
	}
	// A name shaped like another project's deployment address would hide
	// that deployment behind this production.
	if m := deploymentShaped.FindStringSubmatch(label); m != nil && m[1] != project {
		if _, err := a.Store.Project(r.Context(), m[1]); err == nil {
			bad(label + " looks like the address of one of " + m[1] + "'s deployments. Choose another name.")
			return
		}
	}
	old := names.Production(p.Label(), a.Deployer.Domain)
	host := names.Production(label, a.Deployer.Domain)
	if host == old {
		writeJSON(w, http.StatusOK, map[string]any{"host": host, "url": a.Deployer.URL(host)})
		return
	}
	err = a.Store.SetSubdomain(r.Context(), project, sub, host)
	if errors.Is(err, store.ErrTaken) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": host + " is already another project's or a custom domain's address.", "field": "subdomain"})
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	if err := a.Deployer.Sync(r.Context()); err != nil {
		a.Log.Warn("router behind after renaming an address; it will retry", "project", project, "err", err)
	}
	a.event(r.Context(), project, "Changed the address", "Production moved from "+old+" to "+host+".", "")
	a.Log.Info("production address renamed", "project", project, "from", old, "to", host)
	writeJSON(w, http.StatusOK, map[string]any{"host": host, "url": a.Deployer.URL(host)})
}
