package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// The jobs screens show a project's scheduled jobs and each job's runs,
// and run one now. Adding and changing jobs stays in the dashboard's Jobs
// tab, which the screens offer to open.

// projectJobs is a project's jobs as pierd lists them, with what its
// production is: a static site's jobs can only call a path.
type projectJobs struct {
	Jobs       []jobSummary
	Production *struct {
		ID     string `json:"id"`
		Server bool   `json:"server"`
	}
	// unavailable says why this server has no jobs to show, when it
	// can't: an older pierd, or one without the scheduler.
	unavailable string
}

type jobsData struct {
	project string
	jobs    *projectJobs
}

type runsData struct {
	job   int64
	runs  []jobRun
	total int
	fresh bool // the first page again, rather than the next one
	err   error
}

type jobStarted struct {
	job, run int64
	err      error
}

// runsPage is how many runs are asked for at a time.
const runsPage = 25

func jobsPath(project string) string {
	return "/api/projects/" + url.PathEscape(project) + "/jobs"
}

func loadJobs(ctx context.Context, c config, project string) *projectJobs {
	var pj projectJobs
	if err := callContext(ctx, c, http.MethodGet, jobsPath(project), nil, &pj); err != nil {
		return &projectJobs{unavailable: cleanLog(err.Error())}
	}
	return &pj
}

func loadRuns(ctx context.Context, c config, project string, job int64, page, per int) ([]jobRun, int, error) {
	var res struct {
		Runs  []jobRun
		Total int
	}
	path := fmt.Sprintf("%s/%d/runs?page=%d&per=%d", jobsPath(project), job, page, min(per, 100))
	err := callContext(ctx, c, http.MethodGet, path, nil, &res)
	return res.Runs, res.Total, err
}

// keepJobs takes in a project's jobs, keeping the cursor on the same job.
// A refresh that failed keeps the jobs loaded before, rather than saying
// there are none.
func (m *cockpit) keepJobs(project string, pj *projectJobs) {
	old := m.jobs[project]
	if pj.unavailable != "" && old != nil && old.unavailable == "" {
		return
	}
	keep := int64(0)
	if project == m.shown {
		if j, ok := m.selectedJob(); ok {
			keep = j.ID
		}
	}
	if m.jobs == nil {
		m.jobs = map[string]*projectJobs{}
	}
	m.jobs[project] = pj
	if project != m.shown {
		return
	}
	for i, j := range pj.Jobs {
		if j.ID == keep {
			m.jCursor = i
		}
	}
	m.jCursor = min(m.jCursor, max(0, len(pj.Jobs)-1))
	m.scroll()
}

// keepRuns takes in a page of the open job's runs. The first page again
// replaces the runs it covers and keeps the older ones already scrolled
// to; the next page goes on the end. The cursor stays on the same run.
func (m *cockpit) keepRuns(runs []jobRun, total int, fresh bool) {
	keep := int64(0)
	if m.rCursor < len(m.jobRuns) {
		keep = m.jobRuns[m.rCursor].ID
	}
	if fresh {
		older := []jobRun{}
		if len(runs) > 0 {
			last := runs[len(runs)-1].ID
			for _, r := range m.jobRuns {
				if r.ID < last {
					older = append(older, r)
				}
			}
		}
		m.jobRuns = append(runs, older...)
	} else {
		have := map[int64]bool{}
		for _, r := range m.jobRuns {
			have[r.ID] = true
		}
		for _, r := range runs {
			if !have[r.ID] {
				m.jobRuns = append(m.jobRuns, r)
			}
		}
	}
	m.runsTotal, m.runsFor = total, m.jobID
	for i, r := range m.jobRuns {
		if r.ID == keep {
			m.rCursor = i
		}
	}
	m.rCursor = min(m.rCursor, max(0, len(m.jobRuns)-1))
	m.scroll()
}

// shownJobs are the shown project's jobs, once they've loaded.
func (m cockpit) shownJobs() *projectJobs { return m.jobs[m.shown] }

func (m cockpit) selectedJob() (jobSummary, bool) {
	pj := m.shownJobs()
	if pj == nil || m.jCursor >= len(pj.Jobs) {
		return jobSummary{}, false
	}
	return pj.Jobs[m.jCursor], true
}

// openJob is the job the job screen shows, from the project's jobs.
func (m cockpit) openJob() (jobSummary, bool) {
	if pj := m.shownJobs(); pj != nil {
		for _, j := range pj.Jobs {
			if j.ID == m.jobID {
				return j, true
			}
		}
	}
	return jobSummary{}, false
}

// mayHaveJobs says whether the project has jobs, or could: any project
// with a production can call a path on it, and a server can run commands.
func (m cockpit) mayHaveJobs() bool {
	pj := m.shownJobs()
	if pj == nil || pj.unavailable != "" {
		return false
	}
	return len(pj.Jobs) > 0 || pj.Production != nil
}

// jobsLabel is home's row for the jobs, with how many there are.
func (m cockpit) jobsLabel() string {
	if n := len(m.shownJobs().Jobs); n > 0 {
		return fmt.Sprintf("Jobs (%d)", n)
	}
	return "Jobs (none yet)"
}

// openJobs shows the project's jobs, loading them if they aren't yet.
func (m *cockpit) openJobs() tea.Cmd {
	if m.shown == "" {
		return nil
	}
	if pj := m.shownJobs(); pj != nil && pj.unavailable != "" {
		m.say(pj.unavailable, coralTxt)
		return nil
	}
	m.view, m.jobFocus = jobsView, projectPane
	m.actKeys[jobsView] = ""
	m.scroll()
	if m.shownJobs() != nil {
		return nil
	}
	c, project := m.c, m.shown
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return jobsData{project: project, jobs: loadJobs(ctx, c, project)}
	}
}

// showJob opens a job's screen and loads its newest runs.
func (m *cockpit) showJob(id int64) tea.Cmd {
	m.view, m.jobID, m.jobFocus = jobView, id, projectPane
	m.actKeys[jobView] = ""
	if m.runsFor != id {
		m.jobRuns, m.runsTotal, m.rCursor, m.rOff = nil, 0, 0, 0
	}
	return m.fetchRuns(0, true)
}

// fetchRuns asks for a page of the open job's runs: the first again, or
// the next one once the cursor reaches the last run loaded.
func (m *cockpit) fetchRuns(page int, fresh bool) tea.Cmd {
	if m.loadingRuns && !fresh {
		return nil
	}
	m.loadingRuns = true
	c, project, job := m.c, m.shown, m.jobID
	per := runsPage
	if fresh {
		per = max(runsPage, len(m.jobRuns))
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		runs, total, err := loadRuns(ctx, c, project, job, page, per)
		return runsData{job: job, runs: runs, total: total, fresh: fresh, err: err}
	}
}

// moreRuns loads the next page when the cursor has reached the last run
// loaded and there are older ones.
func (m *cockpit) moreRuns() tea.Cmd {
	if m.rCursor < len(m.jobRuns)-1 || len(m.jobRuns) >= m.runsTotal || len(m.jobRuns) == 0 {
		return nil
	}
	return m.fetchRuns(len(m.jobRuns)/runsPage, false)
}

// jobsActions are what the jobs screen offers besides opening a job.
func (m cockpit) jobsActions() []action {
	return []action{{"Open the Jobs tab in the dashboard", "o"}, {"Back to " + m.shown, "esc"}}
}

// jobActions are what the job screen offers: running it now, for anyone
// who may, or following the run that's going; then going back.
func (m cockpit) jobActions() []action {
	var out []action
	j, ok := m.openJob()
	switch {
	case ok && j.Running:
		out = append(out, action{"Show the run that's going", "n"})
	case ok && !m.viewer():
		out = append(out, action{"Run now", "n"})
	}
	return append(out, action{"Back to the jobs", "esc"})
}

// jobsFocused is the part of the jobs or job screen the keys act on. The
// list gives way to the actions when it's empty.
func (m cockpit) jobsFocused() pane {
	n := len(m.jobRuns)
	if m.view == jobsView {
		if pj := m.shownJobs(); pj != nil {
			n = len(pj.Jobs)
		} else {
			n = 0
		}
	}
	if n == 0 {
		return actionsPane
	}
	return m.jobFocus
}

// jobsMove moves through the jobs or the runs, and on into the actions
// under them, the way home's actions and deployments read as one list.
func (m *cockpit) jobsMove(key string) tea.Cmd {
	if m.jobsFocused() == actionsPane {
		if !m.moveAction(key) && (key == "up" || key == "k") && m.listLen() > 0 {
			m.jobFocus = projectPane
		}
		return nil
	}
	cursor := &m.jCursor
	if m.view == jobView {
		cursor = &m.rCursor
	}
	n := m.listLen()
	switch key {
	case "up", "k":
		*cursor--
	case "down", "j":
		if *cursor == n-1 && m.view == jobsView {
			m.jobFocus = actionsPane
			m.actKeys[m.view] = m.actions()[0].key
			return nil
		}
		*cursor++
	case "pgup":
		*cursor -= max(1, m.paneRows()/2)
	case "pgdown":
		*cursor += max(1, m.paneRows()/2)
	case "home", "g":
		*cursor = 0
	case "end", "G":
		*cursor = n - 1
	}
	*cursor = min(max(*cursor, 0), max(0, n-1))
	m.scroll()
	if m.view == jobView {
		return m.moreRuns()
	}
	return nil
}

func (m cockpit) listLen() int {
	if m.view == jobView {
		return len(m.jobRuns)
	}
	if pj := m.shownJobs(); pj != nil {
		return len(pj.Jobs)
	}
	return 0
}

func (m cockpit) jobsKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "q":
		return m, tea.Quit
	case "esc":
		m.view = homeView
		return m, nil
	case "?":
		m.openHelp()
		return m, nil
	case "tab", "shift+tab":
		if m.jobsFocused() == actionsPane && m.listLen() > 0 {
			m.jobFocus = projectPane
		} else {
			m.jobFocus = actionsPane
		}
		return m, nil
	case "up", "k", "down", "j", "pgup", "pgdown", "home", "end", "g", "G":
		return m, m.jobsMove(k.String())
	case "o":
		openBrowser(m.c.Server + "/projects/" + url.PathEscape(m.shown) + "/jobs")
		m.say("Opened the Jobs tab of "+m.shown+" in the dashboard", mint)
		return m, nil
	case "enter":
		if m.jobsFocused() == actionsPane {
			return m.runAction()
		}
		if j, ok := m.selectedJob(); ok {
			return m, m.showJob(j.ID)
		}
	}
	return m, nil
}

func (m cockpit) jobKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "q":
		return m, tea.Quit
	case "esc":
		m.view = jobsView
		return m, nil
	case "?":
		m.openHelp()
		return m, nil
	case "tab", "shift+tab":
		if m.jobsFocused() == actionsPane && m.listLen() > 0 {
			m.jobFocus = projectPane
		} else {
			m.jobFocus = actionsPane
		}
		return m, nil
	case "up", "k", "down", "j", "pgup", "pgdown", "home", "end", "g", "G":
		// The actions sit above the runs, so moving up off the first run
		// reaches them, and moving down off the last action reaches the
		// runs.
		key := k.String()
		if m.jobsFocused() == actionsPane {
			if !m.moveAction(key) && (key == "down" || key == "j") && len(m.jobRuns) > 0 {
				m.jobFocus = projectPane
				m.rCursor = 0
				m.scroll()
			}
			return m, nil
		}
		if (key == "up" || key == "k") && m.rCursor == 0 {
			acts := m.actions()
			m.jobFocus = actionsPane
			m.actKeys[jobView] = acts[len(acts)-1].key
			return m, nil
		}
		return m, m.jobsMove(key)
	case "n":
		return m.runNow()
	case "enter":
		if m.jobsFocused() == actionsPane {
			return m.runAction()
		}
		if m.rCursor < len(m.jobRuns) {
			r := m.jobRuns[m.rCursor]
			return m, m.openLogs(m.runSource(m.jobID, r.ID, r.Status == "running"))
		}
	}
	return m, nil
}

// runNow starts a run of the open job and follows it live, or, when one
// is already going, follows that one.
func (m cockpit) runNow() (tea.Model, tea.Cmd) {
	j, ok := m.openJob()
	if !ok {
		return m, nil
	}
	if j.Running {
		for _, r := range m.jobRuns {
			if r.Status == "running" {
				return m, m.openLogs(m.runSource(j.ID, r.ID, true))
			}
		}
		m.say(j.Name+" is running now. Its run shows here in a moment.", coralTxt)
		return m, m.kick()
	}
	if m.refused("run jobs") {
		return m, nil
	}
	c, project := m.c, m.shown
	m.busy = "Starting " + j.Name
	return m, tea.Batch(m.spinner(), func() tea.Msg {
		var res struct{ Run int64 }
		err := call(c, http.MethodPost, jobsPath(project)+"/"+strconv.FormatInt(j.ID, 10)+"/run", nil, &res)
		return jobStarted{job: j.ID, run: res.Run, err: err}
	})
}

// runSource is a run's output as a log: followed until it ends when it's
// still going.
func (m cockpit) runSource(job, run int64, follow bool) logSource {
	name := ""
	if j, ok := m.openJob(); ok {
		name = j.Name
	}
	return logSource{c: m.c, project: m.shown, kind: "Run", job: job, run: run, jobName: name, follow: follow}
}

// startedHow says how a run started: on its schedule, or with Run now
// and by whom.
func startedHow(r jobRun) string {
	switch {
	case r.StartedBy == "manual" && r.RunBy != "":
		return "Run now by " + r.RunBy
	case r.StartedBy == "manual":
		return "Run now"
	}
	return "on its schedule"
}

// runResult is a run's exit code or HTTP status.
func runResult(r jobRun) string {
	switch {
	case r.HTTPStatus != nil:
		return fmt.Sprintf("HTTP %d", *r.HTTPStatus)
	case r.ExitCode != nil:
		return fmt.Sprintf("exit %d", *r.ExitCode)
	}
	return ""
}

// runTook is how long a run took, or has taken so far.
func runTook(r jobRun) string {
	switch {
	case r.Status == "skipped":
		return ""
	case r.Status == "running" || r.FinishedAt.IsZero():
		return seconds(time.Since(r.StartedAt))
	}
	d := r.FinishedAt.Sub(r.StartedAt)
	if d < time.Second {
		return fmt.Sprintf("%d ms", d.Milliseconds())
	}
	return seconds(d)
}

// lastRunWord is a job's last run in a word: running, succeeded, failed,
// skipped, or nothing yet.
func lastRunWord(j jobSummary) string {
	switch {
	case j.Running:
		return "running"
	case j.LastRun != nil:
		return j.LastRun.Status
	}
	return "not run"
}

// runInk is the colour of a run's word, as the job page shows it.
func runInk(word string) string {
	switch word {
	case "succeeded":
		return mint
	case "failed":
		return coralTxt
	case "running":
		return lavender
	}
	return asleep
}

// nextIn is when a job runs next, short enough for a column.
func nextIn(j jobSummary) string {
	switch {
	case !j.Enabled:
		return "off"
	case j.NextRun == nil:
		return "never"
	}
	d := time.Until(*j.NextRun)
	switch {
	case d < 90*time.Second:
		return "in a minute"
	case d < time.Hour:
		return fmt.Sprintf("in %d min", int(d.Minutes()+0.5))
	case d < 24*time.Hour:
		n := int(d.Hours() + 0.5)
		if n == 1 {
			return "in an hour"
		}
		return fmt.Sprintf("in %d hours", n)
	}
	return j.NextRun.Local().Format("Mon 15:04")
}
