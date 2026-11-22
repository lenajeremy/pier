package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"pier/internal/deploy"
	"pier/internal/jobs"
	"pier/internal/store"
)

func (a *API) jobRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/projects/{project}/jobs", a.auth(http.HandlerFunc(a.listJobs)))
	mux.Handle("POST /api/projects/{project}/jobs", a.auth(http.HandlerFunc(a.saveJob)))
	mux.Handle("GET /api/projects/{project}/jobs/{id}", a.auth(http.HandlerFunc(a.getJob)))
	mux.Handle("PUT /api/projects/{project}/jobs/{id}", a.auth(http.HandlerFunc(a.saveJob)))
	mux.Handle("DELETE /api/projects/{project}/jobs/{id}", a.auth(http.HandlerFunc(a.deleteJob)))
	mux.Handle("POST /api/projects/{project}/jobs/{id}/run", a.auth(http.HandlerFunc(a.runJob)))
	mux.Handle("GET /api/projects/{project}/jobs/{id}/runs", a.auth(http.HandlerFunc(a.jobRuns)))
	mux.Handle("GET /api/projects/{project}/jobs/{id}/runs/{run}", a.auth(http.HandlerFunc(a.jobRun)))
	mux.Handle("POST /api/jobs/schedule", a.auth(http.HandlerFunc(a.describeSchedule)))
}

// jobView is a job as the dashboard and the CLI show it: what it does, when
// it runs, in words, and how its last run went.
type jobView struct {
	store.Job
	Description string        `json:"description"`
	NextRun     *time.Time    `json:"next_run"`
	LastRun     *store.JobRun `json:"last_run"`
	Running     bool          `json:"running"`
}

func (a *API) viewJob(j store.Job, last map[int64]store.JobRun) jobView {
	v := jobView{Job: j, Running: a.Jobs != nil && a.Jobs.Running(j.ID)}
	if s, err := jobs.Parse(j.Schedule); err == nil {
		v.Description = s.Describe(j.Timezone)
		if j.Enabled {
			loc, err := time.LoadLocation(j.Timezone)
			if err != nil {
				loc = time.UTC
			}
			if next := s.Next(time.Now(), loc); !next.IsZero() {
				v.NextRun = &next
			}
		}
	}
	if r, ok := last[j.ID]; ok {
		v.LastRun = &r
	}
	return v
}

func (a *API) jobsReady(w http.ResponseWriter) bool {
	if a.Jobs == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Scheduled jobs aren't available on this server."})
		return false
	}
	return true
}

// listJobs is the project's Jobs tab: every job, and what production is,
// since a static site can't run a command.
func (a *API) listJobs(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	ps, ok := a.findProject(w, r, name)
	if !ok || !a.jobsReady(w) {
		return
	}
	ctx := r.Context()
	list, err := a.Store.Jobs(ctx, name)
	if err != nil {
		a.fail(w, err)
		return
	}
	last, err := a.Store.LastRuns(ctx, name)
	if err != nil {
		a.fail(w, err)
		return
	}
	views := []jobView{}
	paths := false
	for _, j := range list {
		views = append(views, a.viewJob(j, last))
		paths = paths || j.Kind == store.JobPath
	}
	var production map[string]any
	stale := false
	if ps.production != nil && ps.production.Deployment != nil {
		d := ps.production.Deployment
		production = map[string]any{"id": d.ID, "server": d.Runtime != "", "created_at": d.CreatedAt}
		// A deployment built before the cron secret was made doesn't have
		// it, so its app can't check a path job's request yet.
		if paths && d.Runtime != "" {
			sealed, made, err := a.Store.CronSecret(ctx, name)
			stale = err == nil && (sealed == "" || made.After(d.CreatedAt))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"jobs": views, "production": production, "secret_stale": stale, "secret_name": deploy.CronSecretName,
	})
}

func (a *API) findJob(w http.ResponseWriter, r *http.Request) (store.Job, bool) {
	name := r.PathValue("project")
	if _, ok := a.findProject(w, r, name); !ok || !a.jobsReady(w) {
		return store.Job{}, false
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": name + " has no job " + r.PathValue("id")})
		return store.Job{}, false
	}
	j, err := a.Store.JobByID(r.Context(), name, id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": name + " has no job " + r.PathValue("id")})
		return store.Job{}, false
	}
	if err != nil {
		a.fail(w, err)
		return store.Job{}, false
	}
	return j, true
}

func (a *API) getJob(w http.ResponseWriter, r *http.Request) {
	j, ok := a.findJob(w, r)
	if !ok {
		return
	}
	last, err := a.Store.LastRuns(r.Context(), j.Project)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": a.viewJob(j, last)})
}

var jobName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

type jobRequest struct {
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	Schedule       string `json:"schedule"`
	Timezone       string `json:"timezone"`
	Command        string `json:"command"`
	Method         string `json:"method"`
	Path           string `json:"path"`
	TimeoutMinutes int    `json:"timeout_minutes"`
	Enabled        *bool  `json:"enabled"`
}

// saveJob adds a job (POST) or changes one (PUT).
func (a *API) saveJob(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	var j store.Job
	if r.Method == http.MethodPut {
		var ok bool
		if j, ok = a.findJob(w, r); !ok {
			return
		}
	} else {
		if _, ok := a.findProject(w, r, project); !ok || !a.jobsReady(w) {
			return
		}
		j = store.Job{Project: project, Enabled: true}
	}
	var req jobRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 16384)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Send the job as JSON."})
		return
	}
	j.Name, j.Kind = strings.TrimSpace(req.Name), req.Kind
	j.Schedule, j.Timezone = strings.Join(strings.Fields(req.Schedule), " "), strings.TrimSpace(req.Timezone)
	if !jobName.MatchString(j.Name) {
		fieldError(w, "name", "Name it with letters, numbers, dots, dashes or underscores, like nightly-cleanup. pier jobs run finds it by its name.")
		return
	}
	if _, err := jobs.Parse(j.Schedule); err != nil {
		fieldError(w, "schedule", err.Error())
		return
	}
	if j.Timezone == "" {
		j.Timezone = "UTC"
	}
	if _, err := time.LoadLocation(j.Timezone); err != nil || strings.EqualFold(j.Timezone, "local") {
		fieldError(w, "timezone", "That isn't a time zone Pier knows. Use a name like Europe/London, or UTC.")
		return
	}
	minutes := req.TimeoutMinutes
	if minutes == 0 {
		minutes = int(jobs.DefaultTimeout / time.Minute)
	}
	if minutes < 1 || minutes > 60 {
		fieldError(w, "timeout_minutes", "Give it from 1 to 60 minutes.")
		return
	}
	j.TimeoutS = minutes * 60
	if req.Enabled != nil {
		j.Enabled = *req.Enabled
	}
	j.Command, j.Method, j.Path = "", "", ""
	switch j.Kind {
	case store.JobCommand:
		j.Command = strings.TrimSpace(req.Command)
		if j.Command == "" || len(j.Command) > 2000 {
			fieldError(w, "command", "Enter the command to run, like npm run cleanup.")
			return
		}
		if prod, live, err := a.Deployer.Production(r.Context(), project); err == nil && live && prod.Runtime == "" {
			fieldError(w, "kind", project+"'s production is a static site, so there's no image to run a command in. Call a path instead.")
			return
		}
	case store.JobPath:
		j.Method = strings.ToUpper(req.Method)
		if j.Method == "" {
			j.Method = http.MethodGet
		}
		if j.Method != http.MethodGet && j.Method != http.MethodPost {
			fieldError(w, "method", "Choose GET or POST.")
			return
		}
		j.Path = strings.TrimSpace(req.Path)
		if _, err := url.ParseRequestURI(j.Path); err != nil || !strings.HasPrefix(j.Path, "/") || strings.HasPrefix(j.Path, "//") || len(j.Path) > 1024 || strings.ContainsAny(j.Path, " \t\r\n") {
			fieldError(w, "path", "Use a path on production starting with /, like /api/cron.")
			return
		}
	default:
		fieldError(w, "kind", "Choose whether the job runs a command or calls a path.")
		return
	}
	ctx := r.Context()
	var err error
	title := "Changed job"
	if r.Method == http.MethodPut {
		err = a.Store.UpdateJob(ctx, j)
	} else {
		title = "Added job"
		j.ID, err = a.Store.CreateJob(ctx, j)
	}
	if errors.Is(err, store.ErrExists) {
		fieldError(w, "name", project+" already has a job called "+j.Name+".")
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	a.event(ctx, project, title+" "+j.Name, "", "")
	saved, err := a.Store.JobByID(ctx, project, j.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	last, _ := a.Store.LastRuns(ctx, project)
	writeJSON(w, http.StatusOK, map[string]any{"job": a.viewJob(saved, last)})
}

func (a *API) deleteJob(w http.ResponseWriter, r *http.Request) {
	j, ok := a.findJob(w, r)
	if !ok {
		return
	}
	if a.Jobs.Running(j.ID) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": j.Name + " is running now. Delete it once this run ends."})
		return
	}
	runs, _, err := a.Store.Runs(r.Context(), j.ID, 0, 1<<30)
	if err != nil {
		a.fail(w, err)
		return
	}
	if err := a.Store.DeleteJob(r.Context(), j.Project, j.ID); err != nil {
		a.fail(w, err)
		return
	}
	ids := make([]int64, 0, len(runs))
	for _, run := range runs {
		ids = append(ids, run.ID)
	}
	a.Jobs.Forget(j.Project, ids)
	a.event(r.Context(), j.Project, "Deleted job "+j.Name, "", "")
	w.WriteHeader(http.StatusNoContent)
}

// runJob starts a run now, unless the last one is still going.
func (a *API) runJob(w http.ResponseWriter, r *http.Request) {
	j, ok := a.findJob(w, r)
	if !ok {
		return
	}
	id, err := a.Jobs.RunNow(r.Context(), j)
	if errors.Is(err, jobs.ErrRunning) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": j.Name + " is running now. A new run can start once this one ends."})
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"run": id})
}

// jobRuns is one page of a job's runs, newest first.
func (a *API) jobRuns(w http.ResponseWriter, r *http.Request) {
	j, ok := a.findJob(w, r)
	if !ok {
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	per, _ := strconv.Atoi(r.URL.Query().Get("per"))
	if per <= 0 || per > 100 {
		per = 25
	}
	page = max(page, 0)
	runs, total, err := a.Store.Runs(r.Context(), j.ID, page*per, per)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs, "total": total, "page": page, "per": per})
}

// jobRun is one run and its output from line from on, so a run under way
// can be followed by asking for what's new.
func (a *API) jobRun(w http.ResponseWriter, r *http.Request) {
	j, ok := a.findJob(w, r)
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("run"), 10, 64)
	run, err := a.Store.Run(r.Context(), j.Project, j.ID, id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": j.Name + " has no run " + r.PathValue("run")})
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	from, _ := strconv.Atoi(r.URL.Query().Get("from"))
	from = max(from, 0)
	lines, err := jobs.ReadLog(a.Jobs.Logs, j.Project, run.ID, from)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"run": run, "lines": lines, "next": from + len(lines), "done": run.Status != store.RunRunning,
	})
}

// describeSchedule says when a schedule runs, in words, with its next few
// runs, for the preview under the schedule field.
func (a *API) describeSchedule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Schedule string `json:"schedule"`
		Timezone string `json:"timezone"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Send the schedule as JSON."})
		return
	}
	zone := strings.TrimSpace(req.Timezone)
	if zone == "" {
		zone = "UTC"
	}
	loc, err := time.LoadLocation(zone)
	if err != nil || strings.EqualFold(zone, "local") {
		fieldError(w, "timezone", "That isn't a time zone Pier knows. Use a name like Europe/London, or UTC.")
		return
	}
	s, err := jobs.Parse(req.Schedule)
	if err != nil {
		fieldError(w, "schedule", err.Error())
		return
	}
	next := []time.Time{}
	t := time.Now()
	for range 3 {
		if t = s.Next(t, loc); t.IsZero() {
			break
		}
		next = append(next, t)
	}
	writeJSON(w, http.StatusOK, map[string]any{"description": s.Describe(zone), "next": next})
}
