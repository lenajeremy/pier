package api

import (
	"context"
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

	"pier/internal/apphost"
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
	if taken, err := a.Store.SubdomainTaken(r.Context(), name); err != nil || taken {
		if err != nil {
			a.fail(w, err)
			return
		}
		writeJSON(w, http.StatusConflict, map[string]any{"error": name + " is another project's address. Choose another name.", "field": "name"})
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

// deleteProject removes a project, unroutes its addresses, stops its containers
// and images, and removes its saved build artifacts and bare repository.
func (a *API) deleteProject(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	if !validProject(name) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "that isn't a project name"})
		return
	}
	err := a.Store.DeleteProject(r.Context(), name)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "there's no project called " + name})
		return
	}
	if errors.Is(err, store.ErrBuilding) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Wait for the running build to finish before deleting this project."})
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	if err := a.Deployer.Sync(r.Context()); err != nil {
		a.Log.Warn("sync router after project deletion", "project", name, "err", err)
	}
	if err := a.finishFiles(r.Context()); err != nil {
		a.Log.Warn("deferred cleanup after project deletion", "project", name, "err", err)
	}
	if a.Deployer.Repos != "" {
		if err := os.RemoveAll(a.Deployer.RepoPath(name)); err != nil {
			a.Log.Warn("remove repository after project deletion", "project", name, "err", err)
		}
	}
	if a.Jobs != nil {
		a.Jobs.ForgetProject(name)
	}
	a.event(r.Context(), name, "Deleted project", "", "")
	a.Log.Info("project deleted", "project", name)
	w.WriteHeader(http.StatusNoContent)
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
		"project":           p,
		"default_branch":    a.Deployer.ProductionBranch,
		"default_memory_mb": apphost.DefaultMemoryMB,
		"presets":           build.Presets,
		"last_build_plan":   detected,
	})
}

type settingsRequest struct {
	ProductionBranch string         `json:"production_branch"`
	Build            store.Build    `json:"build"`
	Runtime          *store.Runtime `json:"runtime"`
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
	existing, err := a.Store.Project(r.Context(), name)
	if err != nil {
		a.fail(w, err)
		return
	}
	rt := existing.Runtime
	if req.Runtime != nil {
		rt = *req.Runtime
		rt.StartCommand = strings.TrimSpace(rt.StartCommand)
		rt.HealthPath = strings.TrimSpace(rt.HealthPath)
		if len(rt.StartCommand) > 1024 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Start command is too long.", "field": "start_command"})
			return
		}
		if rt.HealthPath != "" {
			if len(rt.HealthPath) > 256 || !strings.HasPrefix(rt.HealthPath, "/") || strings.HasPrefix(rt.HealthPath, "//") || strings.ContainsAny(rt.HealthPath, " \t\r\n") {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Use a local HTTP path starting with /, like /health.", "field": "health_path"})
				return
			}
			if _, err := url.ParseRequestURI(rt.HealthPath); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Use a valid HTTP path starting with /, like /health.", "field": "health_path"})
				return
			}
		}
		if rt.MemoryMB < -1 || (rt.MemoryMB > 0 && (rt.MemoryMB < 64 || rt.MemoryMB > 65536)) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Choose a memory limit between 64 MB and 64 GB, default, or unlimited.", "field": "memory_mb"})
			return
		}
		if rt.CPUs < 0 || (rt.CPUs > 0 && (rt.CPUs < 0.25 || rt.CPUs > 64)) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Choose a CPU limit between 0.25 and 64 vCPU, or unlimited.", "field": "cpus"})
			return
		}
	}
	if err := a.Store.SetProjectSettings(r.Context(), name, req.ProductionBranch, b, rt); err != nil {
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
		Target: target, Files: files, Tools: tools, SentBy: userFrom(r.Context()).DisplayName(),
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
