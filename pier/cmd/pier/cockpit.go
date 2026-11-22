package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"pier/internal/apphost"
	"pier/internal/board"
)

// The cockpit is what pier opens on a terminal: every project down the
// left, the chosen one on the right with its addresses and deployments,
// and single keys to deploy, promote, roll back and read logs. The board,
// a deploy in progress, a deployment's detail, the logs and help are its
// other screens, so moving between them never leaves the program and a
// build keeps being followed while you look at something else.

type view int

const (
	homeView view = iota
	deployView
	boardView
	detailView
	logsView
	helpView
	jobsView // a project's scheduled jobs
	jobView  // one job, and its runs
	viewCount
)

// The panes home's keys can act on: the projects, the project's actions,
// and its deployments. projectPane is the deployments.
type pane int

const (
	projectsPane pane = iota
	projectPane
	actionsPane
)

// A dialog asks before anything changes production, and picks a project
// when the projects pane is hidden.
type dialogKind int

const (
	noDialog dialogKind = iota
	askProduction
	askPromote
	pickRollback
	pickProject
)

// wideEnough is the narrowest terminal that shows the projects pane
// beside the project.
const wideEnough = 90

// leftWidth is the projects pane, borders included.
const leftWidth = 26

type cockpitProject struct {
	Name, Address, URL, Status string
}

type boardRow struct {
	board.Row
	URL string `json:"url"`
}

type projectDetail struct {
	Name           string
	Address        string
	URL            string
	Status         string
	RolledBackFrom string `json:"rolled_back_from"`
	RollbackTo     string `json:"rollback_to"`
	Deployments    []deploymentSummary
}

type deploymentDetail struct {
	Deployment deploymentSummary
	Addresses  []struct {
		Host, URL, Kind string
	}
	Container *apphost.ContainerStatus
	Health    *healthView // production's server only
}

// healthView is how production's server did at its health checks, which
// pierd runs every 30 seconds.
type healthView struct {
	State         string    `json:"state"` // healthy, failing, unhealthy or restarting
	Asked         string    `json:"asked"`
	LastFailure   string    `json:"last_failure"`
	LastFailureAt time.Time `json:"last_failure_at"`
	Restarts      int       `json:"restarts"`
	RestartedAt   time.Time `json:"restarted_at"`
}

// A dialog's choice: a project, a deployment to go back to, or a button.
// Buttons show their key on the right, the others a detail.
type dialogItem struct {
	id, title, detail, ink, key string
}

type cockpit struct {
	c             config
	server        string // the server's host, for the header
	dir, linked   string // this folder, and the project it deploys to
	width, height int
	color         bool

	view, back view
	focus      pane

	projects       []cockpitProject
	pCursor, pOff  int
	pFilter        string
	rows           []boardRow
	bCursor, bOff  int
	shown          string // the project the right pane shows
	details        map[string]*projectDetail
	dCursor, dOff  int
	dFilter        string
	searching      bool
	detail         *deploymentDetail
	detailProject  string
	detailID       string
	detailFromRows bool // Esc from the detail goes back to the board

	dialog    dialogKind
	dlgItems  []dialogItem
	dlgCursor int
	dlgOff    int
	dlgFilter string
	dlgTarget string         // the deployment a promote names
	pending   *pendingDeploy // the deploy a production dialog starts

	// actKeys remembers the focused action on each screen by its key, so
	// focus stays on the same action when the list around it changes.
	actKeys   [viewCount]string
	lastClick click

	run     *deployRun
	runs    int
	logs    logView
	logOpen bool
	logGen  int
	logStop context.CancelFunc
	help    helpModel

	notice, noticeInk string
	busy              string // an action waiting on pierd, shown with a spinner
	spin              int
	spinning          bool
	loading           bool
	tickGen           int
	showGen           int
	offline           error

	deployErr error // what pier deploy returns once the cockpit closes
	ranDeploy bool

	role string // this computer's account's role: owner, member or viewer
	// The newest release pierd has heard of, and pierd's own, for the
	// header's "update available".
	latest, serverVer string

	// The jobs and job screens: each project's jobs as last loaded, the
	// open job's runs as far as they've been scrolled, and which of the
	// list or its actions has the keys.
	jobs          map[string]*projectJobs
	jCursor, jOff int
	jobID         int64
	jobRuns       []jobRun
	runsTotal     int
	runsFor       int64
	rCursor, rOff int
	jobFocus      pane
	loadingRuns   bool
}

type cockpitData struct {
	projects  []cockpitProject
	rows      []boardRow
	role      string
	latest    string // the newest release pierd has heard of
	serverVer string // pierd's own release
	name      string
	detail    *projectDetail
	depKey    string
	dep       *deploymentDetail
	jobs      *projectJobs
	runsJob   int64
	runs      []jobRun
	runsTotal int
	err       error
}

// want is what a refresh asks pierd for beyond the projects and the board:
// the shown project, the open deployment, and the open job's runs, as many
// as have been scrolled to.
type want struct {
	name, depProject, depID string
	job                     int64
	runs                    int
}

type detailData struct {
	name   string
	detail *projectDetail
	jobs   *projectJobs
	err    error
}

type refreshTick struct{ gen int }
type showTick struct{ gen int }
type spinTick struct{}
type actionDone struct {
	notice string
	err    error
	logs   *logSource // open these logs once it's done
}
type deployMsg struct {
	run int
	ev  deployEvent
}
type logMsg struct {
	gen int
	msg tea.Msg
}

// click is the last click, to tell a double click from two clicks.
type click struct {
	kind  hitKind
	index int
	at    time.Time
}

// openCockpit loads what the first screen shows before it opens, so it
// never opens empty, then runs until the owner quits.
func openCockpit(c config, start view, project string, focus pane, dep *pendingDeploy) error {
	m := cockpit{
		c:       c,
		server:  host(c.Server),
		width:   96,
		height:  30,
		color:   os.Getenv("NO_COLOR") == "",
		details: map[string]*projectDetail{},
		jobs:    map[string]*projectJobs{},
		view:    start,
		back:    homeView,
		focus:   focus,
	}
	if dir, err := filepath.Abs("."); err == nil {
		m.dir = dir
	}
	if l, err := readLink("."); err == nil && (l.Server == "" || l.Server == c.Server) {
		m.linked = l.Project
	}
	if project == "" {
		project = m.linked
	}
	if dep != nil {
		project = dep.project
	}
	data := fetchCockpit(c, want{name: project}, true)
	if data.err != nil {
		return data.err
	}
	m.apply(data)
	m.choose(project)
	if dep != nil {
		m.ranDeploy = true
		m.startDeploy(*dep)
	}
	final, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	if err != nil {
		return err
	}
	res := final.(cockpit)
	if res.logStop != nil {
		res.logStop()
	}
	if res.run != nil && res.run.cancel != nil {
		res.run.cancel()
	}
	return res.deployErr
}

// Init starts the refreshing. It can't change the model, so the first
// tick carries the generation the model already has.
func (m cockpit) Init() tea.Cmd {
	gen := m.tickGen
	cmds := []tea.Cmd{tea.Tick(m.every(), func(time.Time) tea.Msg { return refreshTick{gen} })}
	if m.run != nil {
		cmds = append(cmds, m.readDeploy(), spinEvery())
	}
	return tea.Batch(cmds...)
}

// fetchCockpit asks pierd for everything the screens show: the projects,
// the board, and the shown project's and deployment's details.
func fetchCockpit(c config, w want, spinner bool) cockpitData {
	name, depProject, depID := w.name, w.depProject, w.depID
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	get := func(label, path string, into any) error {
		if spinner {
			stop := spin(label)
			defer stop()
		}
		return callContext(ctx, c, http.MethodGet, path, nil, into)
	}
	out := cockpitData{name: name}
	var list struct{ Projects []cockpitProject }
	if out.err = get("Loading your projects", "/api/projects", &list); out.err != nil {
		return out
	}
	out.projects = list.Projects
	// The role is asked for with everything else, so a change the owner
	// makes shows at the next refresh.
	if who, err := whoAmI(ctx, c); err == nil {
		out.role = who.Role
	}
	if v, err := serverInfo(c); err == nil {
		out.latest, out.serverVer = v.Latest, v.Version
	}
	var b struct{ Rows []boardRow }
	if out.err = get("Loading the board", "/api/board", &b); out.err != nil {
		return out
	}
	out.rows = b.Rows
	if name == "" && len(list.Projects) > 0 {
		name = list.Projects[0].Name
		out.name = name
	}
	if name != "" && slices.ContainsFunc(list.Projects, func(p cockpitProject) bool { return p.Name == name }) {
		var d projectDetail
		if out.err = get("Loading "+name, "/api/projects/"+url.PathEscape(name), &d); out.err != nil {
			return out
		}
		out.detail = &d
	}
	if depID != "" {
		var d deploymentDetail
		if err := get("Loading the deployment", "/api/projects/"+url.PathEscape(depProject)+"/deployments/"+url.PathEscape(depID), &d); err == nil {
			out.dep, out.depKey = &d, depProject+"/"+depID
		}
	}
	if out.detail != nil {
		out.jobs = loadJobs(ctx, c, name)
	}
	if w.job != 0 && name != "" {
		if runs, total, err := loadRuns(ctx, c, name, w.job, 0, max(25, w.runs)); err == nil {
			out.runsJob, out.runs, out.runsTotal = w.job, runs, total
		}
	}
	return out
}

// refresh asks again in the background. Only one is out at a time, and
// the one that answers schedules the next.
func (m *cockpit) refresh() tea.Cmd {
	if m.loading {
		return nil
	}
	m.loading = true
	c, w := m.c, m.refreshWant()
	return func() tea.Msg {
		return fetchCockpit(c, w, false)
	}
}

// refreshWant is what the screen on show needs from pierd.
func (m cockpit) refreshWant() want {
	w := want{name: m.shown}
	if m.view == detailView {
		w.depProject, w.depID = m.detailProject, m.detailID
	}
	if m.view == jobView || (m.view == logsView && m.back == jobView) {
		w.job, w.runs = m.jobID, len(m.jobRuns)
	}
	return w
}

// nextTick waits 3 seconds while anything builds or restarts, so the
// change shows soon after it happens, and 15 otherwise.
func (m *cockpit) nextTick() tea.Cmd {
	m.tickGen++
	gen := m.tickGen
	return tea.Tick(m.every(), func(time.Time) tea.Msg { return refreshTick{gen} })
}

func (m cockpit) every() time.Duration {
	if m.active() {
		return 3 * time.Second
	}
	return 15 * time.Second
}

func (m cockpit) active() bool {
	if m.busy != "" || (m.run != nil && !m.run.done) {
		return true
	}
	for _, p := range m.projects {
		if p.Status == "building" {
			return true
		}
	}
	for _, r := range m.rows {
		if r.Status == "building" {
			return true
		}
	}
	if d := m.details[m.shown]; d != nil {
		for _, dep := range d.Deployments {
			if dep.Status == "building" {
				return true
			}
		}
	}
	// A job running is followed the way a build is.
	if pj := m.jobs[m.shown]; pj != nil {
		for _, j := range pj.Jobs {
			if j.Running {
				return true
			}
		}
	}
	for _, r := range m.jobRuns {
		if r.Status == "running" {
			return true
		}
	}
	return false
}

func spinEvery() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return spinTick{} })
}

// apply takes in what pierd answered, keeping every selection on the same
// project, deployment and address it was on, wherever they moved to.
func (m *cockpit) apply(d cockpitData) {
	if d.err != nil {
		m.offline = d.err
		return
	}
	m.offline = nil
	if d.role != "" {
		m.role = d.role
	}
	if d.serverVer != "" {
		m.latest, m.serverVer = d.latest, d.serverVer
	}
	keepProject := ""
	if vis := m.visibleProjects(); m.pCursor < len(vis) {
		keepProject = m.projects[vis[m.pCursor]].Name
	}
	keepRow := ""
	if m.bCursor < len(m.rows) {
		keepRow = m.rows[m.bCursor].Address
	}
	keepDep := m.selectedDeployment()

	m.projects = d.projects
	m.rows = d.rows
	if d.detail != nil {
		m.details[d.name] = d.detail
	}
	if d.jobs != nil {
		m.keepJobs(d.name, d.jobs)
	}
	if d.runsJob != 0 && d.runsJob == m.jobID {
		m.keepRuns(d.runs, d.runsTotal, true)
	}
	if d.dep != nil && d.depKey == m.detailProject+"/"+m.detailID {
		m.detail = d.dep
	}

	if keepProject != "" {
		for i, idx := range m.visibleProjects() {
			if m.projects[idx].Name == keepProject {
				m.pCursor = i
			}
		}
	}
	m.pCursor = min(m.pCursor, max(0, len(m.visibleProjects())-1))
	for i, r := range m.rows {
		if r.Address == keepRow {
			m.bCursor = i
		}
	}
	m.bCursor = min(m.bCursor, max(0, len(m.rows)-1))
	if keepDep != nil {
		for i, idx := range m.visibleDeployments() {
			if m.details[m.shown].Deployments[idx].ID == keepDep.ID {
				m.dCursor = i
			}
		}
	}
	m.dCursor = min(m.dCursor, max(0, len(m.visibleDeployments())-1))
	m.scroll()
}

// choose shows a project on the right, and puts the projects pane's
// cursor on it.
func (m *cockpit) choose(name string) {
	if name == "" && len(m.projects) > 0 {
		name = m.projects[0].Name
	}
	m.pFilter = ""
	for i, p := range m.projects {
		if p.Name == name {
			m.pCursor = i
		}
	}
	if m.shown != name {
		m.dCursor, m.dOff, m.dFilter = 0, 0, ""
	}
	m.shown = name
	m.scroll()
}

func (m cockpit) visibleProjects() []int {
	var out []int
	q := strings.ToLower(strings.TrimSpace(m.pFilter))
	for i, p := range m.projects {
		if q == "" || strings.Contains(strings.ToLower(p.Name), q) {
			out = append(out, i)
		}
	}
	return out
}

func (m cockpit) visibleDeployments() []int {
	d := m.details[m.shown]
	if d == nil {
		return nil
	}
	var out []int
	q := strings.ToLower(strings.TrimSpace(m.dFilter))
	for i, dep := range d.Deployments {
		hay := strings.ToLower(dep.ID + " " + dep.Message + " " + dep.Branch + " " + deploymentWord(dep))
		if q == "" || strings.Contains(hay, q) {
			out = append(out, i)
		}
	}
	return out
}

func (m cockpit) selectedDeployment() *deploymentSummary {
	vis := m.visibleDeployments()
	if m.dCursor >= len(vis) {
		return nil
	}
	return &m.details[m.shown].Deployments[vis[m.dCursor]]
}

// wide says whether the projects pane fits beside the project.
func (m cockpit) wide() bool { return m.width >= wideEnough }

// focused is the pane the keys act on. With the projects pane hidden it
// is always the project.
func (m cockpit) focused() pane {
	switch {
	case m.focus == actionsPane && len(m.shownActions()) == 0:
		return projectPane
	case m.focus == actionsPane:
		return actionsPane
	case !m.wide():
		return projectPane
	}
	return m.focus
}

// panes are the panes Tab goes through on home, in the order they read.
func (m cockpit) panes() []pane {
	var out []pane
	if m.wide() {
		out = append(out, projectsPane)
	}
	if len(m.shownActions()) > 0 {
		out = append(out, actionsPane)
	}
	return append(out, projectPane)
}

// scroll keeps every cursor in view after anything moved or the terminal
// changed size.
func (m *cockpit) scroll() {
	m.pOff = window(m.pCursor, m.pOff, len(m.visibleProjects()), m.paneRows()-2)
	m.dOff = window(m.dCursor, m.dOff, len(m.visibleDeployments()), m.deploymentRows())
	m.bOff = window(m.bCursor, m.bOff, len(m.rows), m.paneRows()-3)
	m.dlgOff = window(m.dlgCursor, m.dlgOff, len(m.dialogVisible()), dialogListRows)
	if pj := m.shownJobs(); pj != nil {
		m.jOff = window(m.jCursor, m.jOff, len(pj.Jobs), m.jobListRows())
	}
	m.rOff = window(m.rCursor, m.rOff, len(m.jobRuns), m.runRows())
}

func (m cockpit) paneRows() int { return bodyRows(m.height) }

func (m *cockpit) say(text, ink string) {
	m.notice, m.noticeInk = text, ink
}

// viewer says whether this computer's account can only look.
func (m cockpit) viewer() bool { return m.role == "viewer" }

// refused tells a viewer, in one sentence, that what they pressed is for
// members, and says so; for anyone else it says nothing and returns false.
func (m *cockpit) refused(does string) bool {
	if !m.viewer() {
		return false
	}
	m.say(viewerCant(does), coralTxt)
	return true
}

func (m *cockpit) fail(err error) {
	if err != nil && !errors.Is(err, errCancelled) {
		m.say(cleanLog(err.Error()), coralTxt)
	}
}

func (m cockpit) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = v.Width, v.Height
		if m.logOpen {
			m.logs.width, m.logs.height = v.Width, v.Height
		}
		m.help.width, m.help.height = v.Width, v.Height
		m.scroll()
		return m, nil
	case refreshTick:
		if v.gen != m.tickGen {
			return m, nil
		}
		if cmd := m.refresh(); cmd != nil {
			return m, cmd
		}
		return m, m.nextTick()
	case cockpitData:
		m.loading = false
		m.apply(v)
		return m, m.nextTick()
	case detailData:
		if v.jobs != nil {
			m.keepJobs(v.name, v.jobs)
		}
		if v.err == nil && v.detail != nil {
			m.details[v.name] = v.detail
			if v.name == m.shown {
				m.dCursor = min(m.dCursor, max(0, len(m.visibleDeployments())-1))
				m.scroll()
			}
		}
		return m, nil
	case detailAnswer:
		if v.detail != nil && v.detail.Name != "" {
			m.details[v.name] = v.detail
		}
		if v.key == m.detailProject+"/"+m.detailID {
			m.detail = v.dep
		}
		m.scroll()
		return m, nil
	case showTick:
		if v.gen != m.showGen {
			return m, nil
		}
		return m, m.fetchDetail(m.shown)
	case spinTick:
		m.spin = (m.spin + 1) % len(spinnerFrames)
		if m.busy != "" || (m.run != nil && !m.run.done) {
			return m, spinEvery()
		}
		m.spinning = false
		return m, nil
	case actionDone:
		m.busy = ""
		if v.err != nil {
			m.fail(v.err)
		} else {
			m.say(v.notice, mint)
		}
		cmds := []tea.Cmd{m.kick()}
		if v.logs != nil {
			cmds = append(cmds, m.openLogs(*v.logs))
		}
		return m, tea.Batch(cmds...)
	case deployMsg:
		if m.run == nil || v.run != m.run.id {
			return m, nil
		}
		return m, m.onDeploy(v.ev)
	case logMsg:
		if !m.logOpen || v.gen != m.logGen {
			return m, nil
		}
		next, cmd := m.logs.Update(v.msg)
		m.logs = next.(logView)
		return m, wrapLog(v.gen, cmd)
	case jobsData:
		if v.jobs != nil {
			m.keepJobs(v.project, v.jobs)
		}
		return m, nil
	case runsData:
		m.loadingRuns = false
		if v.err == nil && v.job == m.jobID {
			m.keepRuns(v.runs, v.total, v.fresh)
		}
		return m, nil
	case jobStarted:
		m.busy = ""
		if v.err != nil {
			m.fail(v.err)
			return m, nil
		}
		return m, tea.Batch(m.openLogs(m.runSource(v.job, v.run, true)), m.kick())
	case tea.KeyMsg:
		return m.key(v)
	case tea.MouseMsg:
		return m.mouse(v)
	}
	return m, nil
}

// kick refreshes now, after something changed, and restarts the timer.
func (m *cockpit) kick() tea.Cmd {
	if m.loading {
		return nil
	}
	m.tickGen++ // the next answer schedules the next refresh
	return m.refresh()
}

func (m *cockpit) fetchDetail(name string) tea.Cmd {
	if name == "" {
		return nil
	}
	c := m.c
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		var d projectDetail
		err := callContext(ctx, c, http.MethodGet, "/api/projects/"+url.PathEscape(name), nil, &d)
		if err != nil {
			return detailData{name: name, err: err}
		}
		// Its jobs come with it, so home's Jobs row shows with the rest.
		return detailData{name: name, detail: &d, jobs: loadJobs(ctx, c, name)}
	}
}

// show puts a project on the right as the cursor reaches it. What was
// loaded before shows at once; pierd is asked again once the cursor
// rests, so holding an arrow key doesn't send a request per project.
func (m *cockpit) show(name string) tea.Cmd {
	if name == m.shown {
		return nil
	}
	m.shown = name
	m.dCursor, m.dOff, m.dFilter = 0, 0, ""
	m.showGen++
	gen := m.showGen
	return tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return showTick{gen} })
}

func (m cockpit) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+c" {
		return m, tea.Quit
	}
	m.notice = ""
	if m.dialog != noDialog {
		return m.dialogKey(k)
	}
	if m.searching {
		return m.searchKey(k)
	}
	switch m.view {
	case logsView:
		return m.logsKey(k)
	case helpView:
		switch k.String() {
		case "esc", "q", "?", "enter":
			m.view = m.back
		default:
			next, _ := m.help.Update(k)
			m.help = next.(helpModel)
		}
		return m, nil
	case deployView:
		return m.deployKey(k)
	case boardView:
		return m.boardKey(k)
	case detailView:
		return m.detailKey(k)
	case jobsView:
		return m.jobsKey(k)
	case jobView:
		return m.jobKey(k)
	}
	return m.homeKey(k)
}

func (m cockpit) homeKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "q":
		return m, tea.Quit
	case "esc":
		// Esc undoes a find; it never quits, since it's also what people
		// press to dismiss whatever just appeared. q quits.
		switch {
		case m.focused() == projectPane && m.dFilter != "":
			m.dFilter, m.dCursor = "", 0
		case m.focused() == projectsPane && m.pFilter != "":
			m.pFilter = ""
			m.choose(m.shown)
		}
		m.scroll()
		return m, nil
	case "tab", "shift+tab":
		if !m.wide() && len(m.panes()) == 1 {
			m.openProjectPicker()
			return m, nil
		}
		// Without the projects pane, Tab picks a project after the
		// actions and deployments have had their turn.
		order := m.panes()
		i := 0
		for j, p := range order {
			if p == m.focused() {
				i = j
			}
		}
		if k.String() == "tab" {
			if !m.wide() && i == len(order)-1 {
				// The picker stands in for the projects pane, so once it
				// closes the focus is where Tab goes after the projects.
				m.focus = order[0]
				m.openProjectPicker()
				return m, nil
			}
			i = (i + 1) % len(order)
		} else {
			i = (i + len(order) - 1) % len(order)
		}
		m.focus = order[i]
		return m, nil
	case "left", "right":
		if m.wide() {
			m.focus = map[string]pane{"left": projectsPane, "right": projectPane}[k.String()]
		}
		return m, nil
	case "up", "k", "down", "j", "pgup", "pgdown", "home", "end", "g", "G":
		return m, m.move(k.String())
	case "enter":
		switch m.focused() {
		case projectsPane:
			m.focus = projectPane
		case actionsPane:
			return m.runAction()
		default:
			if dep := m.selectedDeployment(); dep != nil {
				return m, m.openDetail(m.shown, dep.ID, false)
			}
		}
		return m, nil
	case "/":
		m.searching = true
		if m.focused() == projectsPane {
			m.pFilter = ""
		} else {
			m.dFilter = ""
		}
		return m, nil
	case "?":
		m.openHelp()
		return m, nil
	case "b":
		m.view = boardView
		m.scroll()
		return m, nil
	case "J":
		return m, m.openJobs()
	case "d", "D":
		if m.refused("deploy") {
			return m, nil
		}
		return m.deployKeyHome(k.String() == "D")
	case "p":
		if m.refused("promote deployments") {
			return m, nil
		}
		return m.promoteSelected()
	case "r":
		if m.refused("roll back production") {
			return m, nil
		}
		return m.askRollback()
	case "l":
		if m.shown == "" {
			return m, nil
		}
		if dep := m.selectedDeployment(); dep != nil && m.focused() == projectPane {
			return m, m.openLogs(logSource{c: m.c, project: m.shown, kind: "Build", deployment: dep.ID, limit: 500, follow: true})
		}
		return m, m.openLogs(logSource{c: m.c, project: m.shown, kind: "Request", limit: 100, follow: true,
			query: url.Values{"from": {fmt.Sprint(time.Now().Add(-24 * time.Hour).UnixMilli())}}})
	case "o":
		if m.shown != "" {
			if d := m.details[m.shown]; d != nil && d.URL != "" {
				openBrowser(d.URL)
				m.say("Opened "+d.URL, mint)
			}
		}
		return m, nil
	}
	return m, nil
}

// move steps the focused pane's cursor. On the projects pane the project
// under the cursor is shown on the right as it moves.
func (m *cockpit) move(key string) tea.Cmd {
	step := func(cursor, n, page int) int {
		if n == 0 {
			return 0
		}
		switch key {
		case "up", "k":
			cursor--
		case "down", "j":
			cursor++
		case "pgup":
			cursor -= max(1, page)
		case "pgdown":
			cursor += max(1, page)
		case "home", "g":
			cursor = 0
		case "end", "G":
			cursor = n - 1
		}
		return min(max(cursor, 0), n-1)
	}
	switch m.focused() {
	case projectsPane:
		vis := m.visibleProjects()
		m.pCursor = step(m.pCursor, len(vis), m.paneRows()-2)
		m.scroll()
		if m.pCursor < len(vis) {
			return m.show(m.projects[vis[m.pCursor]].Name)
		}
		return nil
	case actionsPane:
		// The actions sit above the deployments, so moving down off the
		// last action carries on into the deployments.
		if !m.moveAction(key) && (key == "down" || key == "j") && len(m.visibleDeployments()) > 0 {
			m.focus, m.dCursor = projectPane, 0
			m.scroll()
		}
		return nil
	}
	if (key == "up" || key == "k") && m.dCursor == 0 && len(m.shownActions()) > 0 {
		acts := m.shownActions()
		m.focus = actionsPane
		m.actKeys[homeView] = acts[len(acts)-1].key
		return nil
	}
	m.dCursor = step(m.dCursor, len(m.visibleDeployments()), m.deploymentRows())
	m.scroll()
	return nil
}

func (m cockpit) searchKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	projects := m.focused() == projectsPane
	filter := &m.dFilter
	if projects {
		filter = &m.pFilter
	}
	switch k.Type {
	case tea.KeyEsc:
		*filter = ""
		m.searching = false
	case tea.KeyEnter:
		m.searching = false
	case tea.KeyBackspace:
		if r := []rune(*filter); len(r) > 0 {
			*filter = string(r[:len(r)-1])
		}
	case tea.KeyUp, tea.KeyDown:
		cmd := m.move(map[tea.KeyType]string{tea.KeyUp: "up", tea.KeyDown: "down"}[k.Type])
		return m, cmd
	case tea.KeyRunes, tea.KeySpace:
		*filter += singleLine(string(k.Runes))
		if k.Type == tea.KeySpace {
			*filter += " "
		}
	}
	if projects {
		m.pCursor = 0
		m.pOff = 0
		m.scroll()
		if vis := m.visibleProjects(); len(vis) > 0 {
			return m, m.show(m.projects[vis[0]].Name)
		}
		return m, nil
	}
	m.dCursor, m.dOff = 0, 0
	m.scroll()
	return m, nil
}

// deployKeyHome starts a deploy of this folder, which only makes sense on
// the project the folder deploys to.
func (m cockpit) deployKeyHome(production bool) (tea.Model, tea.Cmd) {
	if m.run != nil && !m.run.done {
		m.view = deployView
		m.choose(m.run.project)
		return m, nil
	}
	switch {
	case m.linked == "":
		m.say("This folder isn't linked to a project yet. Run pier deploy here once to choose one.", coralTxt)
		return m, nil
	case m.linked != m.shown:
		m.say("This folder deploys to "+m.linked+". Select "+m.linked+" to deploy it.", coralTxt)
		return m, nil
	}
	p := pendingDeploy{project: m.linked, dir: m.dir, target: "preview"}
	if production {
		p.target = "production"
		m.askProduction(p)
		return m, nil
	}
	return m, m.beginDeploy(p)
}

// askProduction asks before a deploy goes to production.
func (m *cockpit) askProduction(p pendingDeploy) {
	m.pending = &p
	m.openDialog(askProduction, []dialogItem{
		{id: "go", title: "Deploy " + filepath.Base(p.dir) + " to production", key: "enter"},
		{id: "cancel", title: "Cancel", key: "esc"},
	})
}

func (m *cockpit) openDialog(kind dialogKind, items []dialogItem) {
	m.dialog = kind
	m.dlgItems, m.dlgCursor, m.dlgOff, m.dlgFilter = items, 0, 0, ""
}

func (m *cockpit) beginDeploy(p pendingDeploy) tea.Cmd {
	p.g = gitInfo(p.dir)
	m.startDeploy(p)
	return tea.Batch(m.readDeploy(), m.spinner(), m.kick())
}

func (m *cockpit) spinner() tea.Cmd {
	if m.spinning {
		return nil
	}
	m.spinning = true
	return spinEvery()
}

// promoteSelected asks before production serves the selected deployment.
func (m cockpit) promoteSelected() (tea.Model, tea.Cmd) {
	dep := m.selectedDeployment()
	switch {
	case m.focused() != projectPane || dep == nil:
		m.say("Choose a deployment to promote: tab, then ↑↓.", coralTxt)
		return m, nil
	case dep.Role == "production":
		m.say(dep.ID+" is already production.", coralTxt)
		return m, nil
	case dep.Status != "ready":
		m.say(dep.ID+" didn't build, so it can't be promoted.", coralTxt)
		return m, nil
	}
	m.askPromote(dep.ID)
	return m, nil
}

// askRollback lists the deployments production can go back to, the one
// before it first, and an undo when production is rolled back already.
func (m cockpit) askRollback() (tea.Model, tea.Cmd) {
	d := m.details[m.shown]
	if d == nil {
		return m, nil
	}
	items := rollbackChoices(d)
	if len(items) == 0 {
		m.say("There's no earlier production deployment of "+m.shown+" to go back to.", coralTxt)
		return m, nil
	}
	m.openDialog(pickRollback, append(items, dialogItem{id: "cancel", title: "Cancel", key: "esc"}))
	return m, nil
}

// askPromote asks before production serves a deployment.
func (m *cockpit) askPromote(id string) {
	m.dlgTarget = id
	m.openDialog(askPromote, []dialogItem{
		{id: "go", title: "Promote " + id + " to production", key: "enter"},
		{id: "cancel", title: "Cancel", key: "esc"},
	})
}

func (m *cockpit) openProjectPicker() {
	var items []dialogItem
	for _, p := range m.projects {
		items = append(items, dialogItem{id: p.Name, title: p.Name, ink: projectInk(p.Status)})
	}
	m.openDialog(pickProject, items)
	for i, it := range items {
		if it.id == m.shown {
			m.dlgCursor = i
		}
	}
	m.scroll()
}

func (m cockpit) dialogVisible() []int {
	var out []int
	q := strings.ToLower(strings.TrimSpace(m.dlgFilter))
	for i, it := range m.dlgItems {
		if q == "" || strings.Contains(strings.ToLower(it.title), q) {
			out = append(out, i)
		}
	}
	return out
}

const dialogListRows = 8

func (m cockpit) dialogKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	vis := m.dialogVisible()
	switch k.String() {
	case "esc", "q":
		if m.dialog == pickProject && m.dlgFilter != "" && k.String() == "esc" {
			m.dlgFilter, m.dlgCursor = "", 0
			return m, nil
		}
		m.dialog = noDialog
		return m, nil
	case "up", "k", "shift+tab":
		m.dlgCursor = max(0, m.dlgCursor-1)
	case "down", "j", "tab":
		m.dlgCursor = min(max(0, len(vis)-1), m.dlgCursor+1)
	case "enter", "y":
		if k.String() == "y" {
			if m.dialog == pickProject {
				break
			}
			// y says yes, whichever choice has the focus.
			if m.dialog != pickRollback {
				m.dlgCursor = 0
			}
		}
		if m.dlgCursor >= len(vis) {
			return m, nil
		}
		choice := m.dlgItems[vis[m.dlgCursor]]
		kind := m.dialog
		m.dialog = noDialog
		if choice.id == "cancel" {
			return m, nil
		}
		switch kind {
		case askProduction:
			if m.pending != nil {
				return m, m.beginDeploy(*m.pending)
			}
		case askPromote:
			return m, m.promote(m.dlgTarget)
		case pickRollback:
			return m, m.rollback(choice.id)
		case pickProject:
			m.choose(choice.id)
			return m, m.fetchDetail(m.shown)
		}
		return m, nil
	case "home":
		m.dlgCursor = 0
	case "end":
		m.dlgCursor = max(0, len(vis)-1)
	case "backspace":
		if r := []rune(m.dlgFilter); m.dialog == pickProject && len(r) > 0 {
			m.dlgFilter = string(r[:len(r)-1])
			m.dlgCursor = 0
		}
	default:
		if m.dialog == pickProject && k.Type == tea.KeyRunes {
			m.dlgFilter += singleLine(string(k.Runes))
			m.dlgCursor = 0
		}
	}
	m.scroll()
	return m, nil
}

func (m *cockpit) promote(id string) tea.Cmd {
	project, c := m.shown, m.c
	m.busy = "Moving production to " + id
	return tea.Batch(m.spinner(), func() tea.Msg {
		var res productionState
		err := call(c, http.MethodPost, "/api/projects/"+url.PathEscape(project)+"/promote", map[string]string{"id": id}, &res)
		return actionDone{notice: "✓ " + res.Address + " serves " + res.serving() + " now.", err: err}
	})
}

func (m *cockpit) rollback(id string) tea.Cmd {
	project, c := m.shown, m.c
	path, body := "/api/projects/"+url.PathEscape(project)+"/rollback", map[string]string{"id": id}
	m.busy = "Rolling back " + project
	if id == "__undo__" {
		path, body = path+"/undo", map[string]string{}
		m.busy = "Undoing the rollback of " + project
	}
	return tea.Batch(m.spinner(), func() tea.Msg {
		var res productionState
		err := call(c, http.MethodPost, path, body, &res)
		if id == "__undo__" {
			return actionDone{notice: "✓ " + res.Address + " serves " + res.serving() + " again, and production builds go live again.", err: err}
		}
		return actionDone{notice: "✓ " + res.Address + " serves " + res.serving() + " now. New production builds wait until you promote one.", err: err}
	})
}

func (m cockpit) boardKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "q":
		return m, tea.Quit
	case "esc", "b":
		m.view = homeView
		return m, nil
	case "up", "k":
		m.bCursor = max(0, m.bCursor-1)
	case "down", "j":
		m.bCursor = min(max(0, len(m.rows)-1), m.bCursor+1)
	case "pgup":
		m.bCursor = max(0, m.bCursor-max(1, m.paneRows()-3))
	case "pgdown":
		m.bCursor = min(max(0, len(m.rows)-1), m.bCursor+max(1, m.paneRows()-3))
	case "home", "g":
		m.bCursor = 0
	case "end", "G":
		m.bCursor = max(0, len(m.rows)-1)
	case "?":
		m.openHelp()
	case "enter":
		if r, ok := m.boardRow(); ok {
			return m, m.openDetail(r.Project, r.Deployment, true)
		}
	case "o":
		if r, ok := m.boardRow(); ok && r.URL != "" {
			openBrowser(r.URL)
			m.say("Opened "+r.URL, mint)
		}
	case "l":
		if r, ok := m.boardRow(); ok {
			if r.Status == "building" || r.Status == "failed" {
				return m, m.openLogs(logSource{c: m.c, project: r.Project, kind: "Build", deployment: r.Deployment, limit: 500, follow: true})
			}
			return m, m.openLogs(logSource{c: m.c, project: r.Project, kind: "Request", limit: 100, follow: true,
				query: url.Values{"from": {fmt.Sprint(time.Now().Add(-24 * time.Hour).UnixMilli())}}})
		}
	}
	m.scroll()
	return m, nil
}

func (m cockpit) boardRow() (boardRow, bool) {
	if m.bCursor < len(m.rows) {
		return m.rows[m.bCursor], true
	}
	return boardRow{}, false
}

// openDetail shows one deployment in the project pane's place.
func (m *cockpit) openDetail(project, id string, fromBoard bool) tea.Cmd {
	if id == "" {
		return nil
	}
	m.detailProject, m.detailID, m.detailFromRows = project, id, fromBoard
	m.detail = nil
	m.actKeys[detailView] = ""
	m.view = detailView
	if project != m.shown {
		m.choose(project)
	}
	c := m.c
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		var d deploymentDetail
		if err := callContext(ctx, c, http.MethodGet, "/api/projects/"+url.PathEscape(project)+"/deployments/"+url.PathEscape(id), nil, &d); err != nil {
			return actionDone{err: err}
		}
		var pd projectDetail
		_ = callContext(ctx, c, http.MethodGet, "/api/projects/"+url.PathEscape(project), nil, &pd)
		return detailAnswer{name: project, detail: &pd, key: project + "/" + id, dep: &d}
	}
}

// detailAnswer is one project's and one deployment's details, merged into
// what the cockpit already has.
type detailAnswer struct {
	name   string
	detail *projectDetail
	key    string
	dep    *deploymentDetail
}

func (m cockpit) detailKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	d := m.detail
	switch k.String() {
	case "q":
		return m, tea.Quit
	case "esc":
		m.view = homeView
		if m.detailFromRows {
			m.view = boardView
		}
		return m, nil
	case "?":
		m.openHelp()
		return m, nil
	case "up", "k", "down", "j", "home", "end", "g", "G", "pgup", "pgdown":
		m.moveAction(k.String())
		return m, nil
	case "enter":
		return m.runAction()
	}
	if d == nil {
		return m, nil
	}
	dep := d.Deployment
	project := m.detailProject
	switch k.String() {
	case "o":
		if u := deploymentURL(d); u != "" {
			openBrowser(u)
			m.say("Opened "+u, mint)
		}
	case "c":
		m.copyAddress(deploymentURL(d))
	case "p":
		switch {
		case m.refused("promote deployments"):
		case dep.Role == "production":
			m.say(dep.ID+" is already production.", coralTxt)
		case dep.Status != "ready":
			m.say(dep.ID+" didn't build, so it can't be promoted.", coralTxt)
		default:
			m.shown = project
			m.askPromote(dep.ID)
		}
	case "r":
		if m.refused("roll back production") {
			return m, nil
		}
		m.shown = project
		return m.askRollback()
	case "l":
		return m, m.openLogs(logSource{c: m.c, project: project, kind: "Build", deployment: dep.ID, limit: 500, follow: true})
	case "s":
		if dep.Runtime == "" {
			m.say(dep.ID+" is a static site, so it has no server output.", coralTxt)
			return m, nil
		}
		return m, m.openLogs(logSource{c: m.c, project: project, kind: "Runtime", deployment: dep.ID, limit: 200, follow: true})
	case "R":
		if m.refused("restart a deployment") {
			return m, nil
		}
		if dep.Runtime == "" || dep.Status != "ready" {
			m.say("Only a running server can be restarted.", coralTxt)
			return m, nil
		}
		c := m.c
		m.busy = "Restarting " + dep.ID + " and waiting for it to answer"
		return m, tea.Batch(m.spinner(), m.kick(), func() tea.Msg {
			err := call(c, http.MethodPost, "/api/projects/"+url.PathEscape(project)+"/deployments/"+url.PathEscape(dep.ID)+"/restart", map[string]string{}, nil)
			return actionDone{notice: "✓ Restarted " + dep.ID + ", and it answered its readiness check.", err: err}
		})
	case "a":
		if m.refused("redeploy") {
			return m, nil
		}
		if dep.Status == "building" {
			m.say(dep.ID+" is still building.", coralTxt)
			return m, nil
		}
		c := m.c
		m.busy = "Starting a new build of " + dep.ID
		return m, tea.Batch(m.spinner(), func() tea.Msg {
			var res struct{ ID string }
			err := call(c, http.MethodPost, "/api/projects/"+url.PathEscape(project)+"/redeploy", map[string]string{"id": dep.ID}, &res)
			if err != nil || res.ID == "" {
				return actionDone{err: err}
			}
			return actionDone{notice: "✓ Building " + res.ID + " from " + dep.ID + "'s source.",
				logs: &logSource{c: c, project: project, kind: "Build", deployment: res.ID, limit: 500, follow: true}}
		})
	}
	return m, nil
}

func deploymentURL(d *deploymentDetail) string {
	if d.Deployment.Status != "ready" {
		return ""
	}
	if len(d.Addresses) > 0 && d.Addresses[0].URL != "" {
		return d.Addresses[0].URL
	}
	return d.Deployment.URL
}

func (m *cockpit) openHelp() {
	m.back = m.view
	m.help = helpModel{server: m.server, project: m.shown, width: m.width, height: m.height, color: m.color}
	m.view = helpView
}

// openLogs follows a log in the cockpit, in the screen's place, until Esc.
func (m *cockpit) openLogs(s logSource) tea.Cmd {
	if m.logStop != nil {
		m.logStop()
	}
	if m.view != logsView {
		m.back = m.view
	}
	in, stop := streamLogs(s)
	m.logGen++
	m.logStop = stop
	m.logs = logView{source: s, in: in, width: m.width, height: m.height, color: m.color}
	m.logOpen = true
	m.view = logsView
	// The mouse goes off on the logs, so their lines can be selected and
	// copied the way a terminal usually allows.
	return tea.Batch(wrapLog(m.logGen, m.logs.Init()), tea.DisableMouse)
}

func wrapLog(gen int, cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg { return logMsg{gen: gen, msg: cmd()} }
}

func (m cockpit) logsKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc", "q":
		if m.logStop != nil {
			m.logStop()
			m.logStop = nil
		}
		m.logOpen = false
		m.logGen++
		m.view = m.back
		// Coming back from a run's output, its job shows how it ended.
		return m, tea.Batch(tea.EnableMouseCellMotion, m.kick())
	case "r":
		if m.logs.ended {
			return m, m.openLogs(m.logs.source)
		}
	}
	next, cmd := m.logs.Update(k)
	m.logs = next.(logView)
	return m, wrapLog(m.logGen, cmd)
}

func (m cockpit) deployKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	r := m.run
	switch k.String() {
	case "q":
		return m, tea.Quit
	case "esc":
		m.view = homeView
		return m, m.kick()
	case "up", "k", "down", "j", "home", "end", "g", "G":
		m.moveAction(k.String())
	case "enter":
		return m.runAction()
	case "o":
		if r.liveURL != "" {
			openBrowser(r.liveURL)
			m.say("Opened "+r.liveURL, mint)
		}
	case "c":
		m.copyAddress(r.liveURL)
	case "d":
		// A failed deploy can be tried again from here, the same way.
		if r.done && r.err != nil {
			p := pendingDeploy{project: r.project, dir: r.dir, target: r.target}
			if p.target == "production" {
				m.askProduction(p)
				return m, nil
			}
			return m, m.beginDeploy(p)
		}
	case "l":
		return m, m.openLogs(logSource{c: m.c, project: r.project, kind: "Build", deployment: r.deployment, limit: 500, follow: true})
	case "?":
		m.openHelp()
	}
	return m, nil
}
