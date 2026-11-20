package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"

	"pier/internal/build"
	"pier/internal/deploy"
	"pier/internal/names"
	"pier/internal/store"
)

// MaxUpload is the largest gzipped folder pier deploy may send.
const MaxUpload = 512 << 20

// reserved names can't be projects: they'd take an address Pier uses.
func (a *API) reserved(name string) bool {
	if name == "www" || name == "pier" && strings.HasPrefix(a.Deployer.PierHost, "pier.") {
		return true
	}
	if label, rest, ok := strings.Cut(a.Deployer.PierHost, "."); ok && rest == a.Deployer.Domain && label == name {
		return true
	}
	return false
}

type createRequest struct {
	Name string `json:"name"`
}

// createProject makes an empty project, ready for pier deploy.
func (a *API) createProject(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "send the project's name"})
		return
	}
	name := strings.TrimSpace(req.Name)
	switch {
	case !names.ValidProject(name):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Use lowercase letters, numbers and single dashes, like my-site.", "field": "name"})
		return
	case a.reserved(name):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": name + " is the dashboard's own address. Choose another name.", "field": "name"})
		return
	}
	err := a.Store.CreateProject(r.Context(), store.Project{Name: name})
	if errors.Is(err, store.ErrExists) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "There's already a project called " + name + ".", "field": "name"})
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	a.Log.Info("project created", "project", name)
	host := names.Production(name, a.Deployer.Domain)
	writeJSON(w, http.StatusCreated, map[string]any{"name": name, "address": host, "url": a.Deployer.URL(host)})
}

// settings is a project's build settings, with what the last build detected
// so the dashboard can show it beside each field.
func (a *API) settings(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	ps, ok := a.findProject(w, r, name)
	if !ok {
		return
	}
	p, err := a.Store.Project(r.Context(), name)
	if err != nil {
		a.fail(w, err)
		return
	}
	var detected map[string]string
	for _, d := range ps.deployments {
		if d.Plan != "" {
			json.Unmarshal([]byte(d.Plan), &detected)
			detected["deployment"] = d.ID
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"project":         p,
		"default_branch":  a.Deployer.ProductionBranch,
		"presets":         build.Presets,
		"last_build_plan": detected,
	})
}

type settingsRequest struct {
	ProductionBranch string      `json:"production_branch"`
	Build            store.Build `json:"build"`
}

func (a *API) saveSettings(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	if _, ok := a.findProject(w, r, name); !ok {
		return
	}
	var req settingsRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 16384)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "send the settings as JSON"})
		return
	}
	b := req.Build
	for _, f := range []*string{&req.ProductionBranch, &b.Root, &b.Preset, &b.Install, &b.Command, &b.Output} {
		*f = strings.TrimSpace(*f)
	}
	if b.Preset != "" && !slices.Contains(build.Presets, b.Preset) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Choose a supported preset or let Pier detect it.", "field": "preset"})
		return
	}
	if _, err := build.Folder(b.Root); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Use a folder inside the project, like web.", "field": "root"})
		return
	}
	if _, err := build.Folder(b.Output); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Use a folder inside the project, like dist.", "field": "output"})
		return
	}
	if strings.ContainsAny(req.ProductionBranch, " ~^:?*[\\") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "That isn't a branch name.", "field": "production_branch"})
		return
	}
	if err := a.Store.SetProjectBuild(r.Context(), name, req.ProductionBranch, b); err != nil {
		a.fail(w, err)
		return
	}
	a.settings(w, r)
}

// upload builds a folder sent by pier deploy, and narrates the build back
// to the CLI as it happens. The build carries on if the CLI disconnects.
func (a *API) upload(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	if _, ok := a.findProject(w, r, name); !ok {
		return
	}
	q := r.URL.Query()
	target := q.Get("target")
	if target != store.TargetProduction && target != store.TargetPreview {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "target must be production or preview"})
		return
	}
	files, _ := strconv.Atoi(q.Get("files"))
	tools := q.Get("tools")
	if !build.ValidTools(tools) {
		tools = ""
	}
	tmp, err := os.CreateTemp(a.Deployer.Tmp, "upload-*.tar.gz")
	if err != nil {
		a.fail(w, err)
		return
	}
	defer os.Remove(tmp.Name())
	n, err := io.Copy(tmp, http.MaxBytesReader(w, r.Body, MaxUpload))
	tmp.Close()
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": fmt.Sprintf("the upload is over %d MB; leave build output and dependencies out", MaxUpload>>20)})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "the upload didn't arrive whole: " + err.Error()})
		return
	}
	a.Log.Info("upload", "project", name, "target", target, "bytes", n)

	src := deploy.Source{
		Branch: clip(q.Get("branch"), 200), Commit: clip(q.Get("commit"), 64), Message: clip(q.Get("message"), 300),
		Target: target, Files: files, Tools: tools,
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Trailer", "Pier-Result")
	w.WriteHeader(http.StatusOK)
	out := &flusher{w: w}
	err = a.Deployer.DeployUpload(context.WithoutCancel(r.Context()), name, tmp.Name(), src, out)
	if err != nil {
		a.Log.Info("deploy failed", "project", name, "err", err)
		w.Header().Set("Pier-Result", "failed")
		return
	}
	w.Header().Set("Pier-Result", "ok")
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}
