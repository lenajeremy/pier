package api

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"pier/internal/deploy"
	"pier/internal/names"
	"pier/internal/store"
)

func validProject(name string) bool { return names.ValidProject(name) }

// Deployment roles: what a deployment is to its project right now.
const (
	RoleProduction = "production" // production's address points at it
	RolePreview    = "preview"    // a branch's address points at it
	RoleBuilding   = "building"
	RoleFailed     = "failed"
	RoleKept       = "kept" // built, and still reachable at its own address
)

// Deployment is a deployment as the dashboard shows it.
type Deployment struct {
	store.Deployment
	Role string `json:"role"`
	Host string `json:"host"` // its own permanent address; empty if it didn't build
	URL  string `json:"url"`
}

// Address is one address of a project and what it serves.
type Address struct {
	Host       string      `json:"host"`
	URL        string      `json:"url"`
	Kind       string      `json:"kind"` // production, branch or deployment
	Branch     string      `json:"branch,omitempty"`
	UpdatedAt  time.Time   `json:"updated_at"`
	Deployment *Deployment `json:"deployment,omitempty"`
}

// projectState is everything known about one project, worked out once.
type projectState struct {
	name             string
	productionBranch string
	deployments      []Deployment // newest first
	production       *Address
	branches         []Address // newest first
}

func (a *API) loadProject(r *http.Request, name string, aliases []store.Alias) (projectState, error) {
	ps := projectState{name: name, productionBranch: a.Deployer.ProductionBranch}
	if p, err := a.Store.Project(r.Context(), name); err == nil && p.ProductionBranch != "" {
		ps.productionBranch = p.ProductionBranch
	}
	ds, err := a.Store.Deployments(r.Context(), name)
	if err != nil {
		return ps, err
	}
	for i := range ds {
		if ds[i].Target == "" { // built before deployments had a target
			ds[i].Target = store.TargetPreview
			if ds[i].Branch == ps.productionBranch {
				ds[i].Target = store.TargetProduction
			}
		}
	}
	pointed := map[string]string{} // deployment ID -> role
	for _, al := range aliases {
		if al.Project != name {
			continue
		}
		if al.Kind == store.KindProduction {
			pointed[al.DeploymentID] = RoleProduction
		} else if pointed[al.DeploymentID] == "" {
			pointed[al.DeploymentID] = RolePreview
		}
	}
	byID := map[string]int{}
	for _, d := range ds {
		dep := Deployment{Deployment: d}
		switch {
		case d.Status == store.Building:
			dep.Role = RoleBuilding
		case d.Status == store.Failed:
			dep.Role = RoleFailed
		case pointed[d.ID] != "":
			dep.Role = pointed[d.ID]
		default:
			dep.Role = RoleKept
		}
		if d.Status == store.Ready {
			dep.Host = names.Deployment(name, d.ID, a.Deployer.Domain)
			dep.URL = a.Deployer.URL(dep.Host)
		}
		byID[d.ID] = len(ps.deployments)
		ps.deployments = append(ps.deployments, dep)
	}
	for _, al := range aliases {
		if al.Project != name {
			continue
		}
		addr := Address{Host: al.Host, URL: a.Deployer.URL(al.Host), Kind: al.Kind, Branch: al.Branch, UpdatedAt: al.UpdatedAt}
		if i, ok := byID[al.DeploymentID]; ok {
			d := ps.deployments[i]
			addr.Deployment = &d
		}
		if al.Kind == store.KindProduction {
			ps.production = &addr
		} else {
			ps.branches = append(ps.branches, addr)
		}
	}
	sort.SliceStable(ps.branches, func(i, j int) bool { return ps.branches[i].UpdatedAt.After(ps.branches[j].UpdatedAt) })
	// The latest preview is a preview even when no branch points at it, as
	// with pier deploy's previews, which only have their own address.
	if lp := ps.latestPreview(); lp != nil && lp.Deployment != nil {
		if i, ok := byID[lp.Deployment.ID]; ok && ps.deployments[i].Role == RoleKept {
			ps.deployments[i].Role = RolePreview
			lp.Deployment.Role = RolePreview
		}
	}
	return ps, nil
}

// latestPreview is the newest preview that built: at its branch's address
// if a branch points at it, or else at its own.
func (ps projectState) latestPreview() *Address {
	for _, d := range ps.deployments {
		if d.Target != store.TargetPreview || d.Status != store.Ready {
			continue
		}
		for _, b := range ps.branches {
			if b.Deployment != nil && b.Deployment.ID == d.ID {
				return &b
			}
		}
		dep := d
		return &Address{Host: d.Host, URL: d.URL, Kind: "deployment", Branch: d.Branch, UpdatedAt: d.CreatedAt, Deployment: &dep}
	}
	return nil
}

// status sums a project up in one word, for its swatch and its row:
// building, live, failed (the newest production build failed, or nothing
// ever built), or none.
func (ps projectState) status() string {
	for _, d := range ps.deployments {
		if d.Status == store.Building {
			return "building"
		}
	}
	for _, d := range ps.deployments {
		if d.Target == store.TargetProduction {
			if d.Status == store.Failed {
				return "failed"
			}
			break
		}
	}
	if ps.production != nil {
		return "live"
	}
	if len(ps.deployments) > 0 && ps.deployments[0].Status == store.Failed {
		return "failed"
	}
	return "none"
}

func (a *API) projects(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	names_, err := a.Store.Projects(ctx)
	if err != nil {
		a.fail(w, err)
		return
	}
	aliases, err := a.Store.Aliases(ctx)
	if err != nil {
		a.fail(w, err)
		return
	}
	out := []map[string]any{}
	for _, name := range names_ {
		ps, err := a.loadProject(r, name, aliases)
		if err != nil {
			a.fail(w, err)
			return
		}
		host := names.Production(name, a.Deployer.Domain)
		p := map[string]any{
			"name": name, "address": host, "url": a.Deployer.URL(host),
			"status": ps.status(), "deployments": len(ps.deployments),
			"latest": nil, "production": nil,
		}
		if len(ps.deployments) > 0 {
			p["latest"] = ps.deployments[0]
		}
		if ps.production != nil {
			p["production"] = ps.production.Deployment
		}
		out = append(out, p)
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": out})
}

// project is everything the project's Deployments tab shows.
func (a *API) project(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	ps, ok := a.findProject(w, r, name)
	if !ok {
		return
	}
	host := names.Production(name, a.Deployer.Domain)
	writeJSON(w, http.StatusOK, map[string]any{
		"name": name, "address": host, "url": a.Deployer.URL(host),
		"production_branch": ps.productionBranch,
		"status":            ps.status(),
		"production":        ps.production,
		"preview":           ps.latestPreview(),
		"branches":          nonNil(ps.branches),
		"deployments":       nonNil(ps.deployments),
		"stats":             buildStats(ps.deployments),
	})
}

// findProject loads a project, or answers 404 if there's no such project.
func (a *API) findProject(w http.ResponseWriter, r *http.Request, name string) (projectState, bool) {
	if !validProject(name) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "that isn't a project name"})
		return projectState{}, false
	}
	all, err := a.Store.Projects(r.Context())
	if err != nil {
		a.fail(w, err)
		return projectState{}, false
	}
	if !slices.Contains(all, name) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "there's no project called " + name})
		return projectState{}, false
	}
	aliases, err := a.Store.Aliases(r.Context())
	if err != nil {
		a.fail(w, err)
		return projectState{}, false
	}
	ps, err := a.loadProject(r, name, aliases)
	if err != nil {
		a.fail(w, err)
		return projectState{}, false
	}
	return ps, true
}

type buildBar struct {
	ID     string `json:"id"`
	MS     int64  `json:"ms"`
	Status string `json:"status"`
}

// buildStats feeds the summary strip: the last 14 finished builds, their
// median time, and what's kept on disk.
func buildStats(ds []Deployment) map[string]any {
	var bars []buildBar
	var kept int
	var bytes int64
	for _, d := range ds {
		if d.Status == store.Ready {
			kept++
			bytes += d.Bytes
		}
		if d.Status != store.Building && len(bars) < 14 {
			bars = append(bars, buildBar{ID: d.ID, MS: d.BuildMS, Status: d.Status})
		}
	}
	slices.Reverse(bars) // oldest first, as the bars read left to right
	var times []int64
	failed := 0
	for _, b := range bars {
		times = append(times, b.MS)
		if b.Status == store.Failed {
			failed++
		}
	}
	var median int64
	if len(times) > 0 {
		slices.Sort(times)
		median = times[len(times)/2]
		if len(times)%2 == 0 {
			median = (times[len(times)/2-1] + times[len(times)/2]) / 2
		}
	}
	return map[string]any{
		"builds": nonNil(bars), "median_ms": median, "failed": failed,
		"kept": kept, "bytes": bytes,
	}
}

// deployments is the CLI's list of a project's deployments, newest first.
func (a *API) deployments(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	ps, ok := a.findProject(w, r, name)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"project": name, "deployments": nonNil(ps.deployments)})
}

// deployment is one deployment's page: its details and every address that
// serves it.
func (a *API) deployment(w http.ResponseWriter, r *http.Request) {
	name, id := r.PathValue("project"), r.PathValue("id")
	ps, ok := a.findProject(w, r, name)
	if !ok {
		return
	}
	i := slices.IndexFunc(ps.deployments, func(d Deployment) bool { return d.ID == id })
	if i < 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": name + " has no deployment " + id})
		return
	}
	d := ps.deployments[i]
	addrs := []Address{}
	if ps.production != nil && ps.production.Deployment != nil && ps.production.Deployment.ID == id {
		addrs = append(addrs, Address{Host: ps.production.Host, URL: ps.production.URL, Kind: store.KindProduction, UpdatedAt: ps.production.UpdatedAt})
	}
	for _, b := range ps.branches {
		if b.Deployment != nil && b.Deployment.ID == id {
			addrs = append(addrs, Address{Host: b.Host, URL: b.URL, Kind: store.KindBranch, Branch: b.Branch, UpdatedAt: b.UpdatedAt})
		}
	}
	if d.Host != "" {
		addrs = append(addrs, Address{Host: d.Host, URL: d.URL, Kind: "deployment"})
	}
	writeJSON(w, http.StatusOK, map[string]any{"deployment": d, "addresses": addrs})
}

// deploymentLog returns a build's log from line `from` onwards, so the
// dashboard can follow a build by asking for what's new.
func (a *API) deploymentLog(w http.ResponseWriter, r *http.Request) {
	name, id := r.PathValue("project"), r.PathValue("id")
	ps, ok := a.findProject(w, r, name)
	if !ok {
		return
	}
	i := slices.IndexFunc(ps.deployments, func(d Deployment) bool { return d.ID == id })
	if i < 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": name + " has no deployment " + id})
		return
	}
	from, _ := strconv.Atoi(r.URL.Query().Get("from"))
	lines, err := a.Deployer.ReadLog(id, max(from, 0))
	if errors.Is(err, deploy.ErrNoLog) {
		lines, err = nil, nil
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"lines": nonNil(lines),
		"next":  max(from, 0) + len(lines),
		"done":  ps.deployments[i].Status != store.Building,
	})
}

// favicon serves the icon of the project's production deployment, so the
// dashboard shows each project with its own icon. 404 when it has none.
func (a *API) favicon(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	ps, ok := a.findProject(w, r, name)
	if !ok {
		return
	}
	if ps.production == nil || ps.production.Deployment == nil {
		http.NotFound(w, r)
		return
	}
	root := filepath.Join(a.Deployer.Deployments, ps.production.Deployment.ID)
	for _, f := range []string{"favicon.svg", "favicon.png", "favicon.ico", "apple-touch-icon.png", "icon.svg", "icon.png"} {
		p := filepath.Join(root, f)
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() && fi.Size() < 1<<20 {
			w.Header().Set("Cache-Control", "private, max-age=300")
			if strings.HasSuffix(f, ".svg") {
				// An SVG can carry script; as an <img> it can't run, and this
				// stops it running if the file is opened directly.
				w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
			}
			http.ServeFile(w, r, p)
			return
		}
	}
	http.NotFound(w, r)
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
