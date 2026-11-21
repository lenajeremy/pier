package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func (m cockpit) View() string {
	w, h := m.width, m.height
	var body []string
	var bar string
	project := m.shown
	switch m.view {
	case logsView:
		return m.logs.View()
	case helpView:
		return m.help.View()
	case boardView:
		project = "every project"
		body = m.boardPane(w, m.paneRows())
		bar = keyBar([]keyHelp{{"↑↓", "move"}, {"enter", "deployment"}, {"o", "open"}, {"l", "logs"}, {"esc", "back"}}, w, m.color)
	default:
		right, keys := m.rightPane()
		if m.wide() {
			body = beside(m.projectsPane(m.paneRows()), right, leftWidth, w-leftWidth-1)
		} else {
			body = right
		}
		bar = keyBar(keys, w, m.color)
	}
	if m.searching {
		bar = m.searchBar(w)
	}
	switch {
	case m.busy != "":
		bar = noticeBar(spinnerFrames[m.spin]+" "+m.busy+"…", lavender, w, m.color)
	case m.notice != "":
		bar = noticeBar(m.notice, m.noticeInk, w, m.color)
	}
	if m.dialog != noDialog {
		var rows []string
		var keys []keyHelp
		rows, keys = m.dialogBox()
		bar = keyBar(keys, w, m.color)
		dw := 0
		if len(rows) > 0 {
			dw = ansi.StringWidth(rows[0])
		}
		x := max(0, (w-dw)/2)
		if m.wide() {
			x = leftWidth + 1 + max(0, (w-leftWidth-1-dw)/2)
		}
		y := max(0, (len(body)-len(rows))/3)
		body = overlay(padRows(body, m.paneRows()), rows, x, y, w)
	}
	right, rightInk := "", ""
	if m.offline != nil {
		right, rightInk = "can't reach "+m.server, coralTxt
	}
	return screen(header(w, project, m.server, right, rightInk, m.color), body, bar, w, h)
}

func padRows(rows []string, n int) []string {
	for len(rows) < n {
		rows = append(rows, "")
	}
	return rows
}

// rightPane is whatever the screen shows beside the projects: the
// project, a deploy, or one deployment; and the keys that go with it.
func (m cockpit) rightPane() ([]string, []keyHelp) {
	rows := m.paneRows()
	rw := m.width
	if m.wide() {
		rw = m.width - leftWidth - 1
	}
	switch m.view {
	case deployView:
		return m.deployPane(rw, rows)
	case detailView:
		return m.detailPane(rw, rows)
	}
	return m.projectPane(rw, rows)
}

func (m cockpit) searchBar(w int) string {
	filter := m.dFilter
	if m.focused() == projectsPane {
		filter = m.pFilter
	}
	line := " " + sgr("/", fgSecondary, "", true, m.color) + " " + sgr(filter, fgBright, "", false, m.color) + sgr("▏", pink, "", false, m.color)
	keys := keyBar([]keyHelp{{"enter", "keep"}, {"esc", "clear"}}, w-ansi.StringWidth(line)-4, m.color)
	return line + "    " + strings.TrimPrefix(keys, " ")
}

// projectsPane lists every project with a dot for what its production is
// doing. The project shown on the right keeps the selected row's colour.
func (m cockpit) projectsPane(rows int) []string {
	focused := m.focused() == projectsPane && m.view == homeView && m.dialog == noDialog
	title := "Projects"
	if m.pFilter != "" {
		title = "Projects matching " + m.pFilter
	}
	inner := leftWidth - 4
	vis := m.visibleProjects()
	var lines []string
	switch {
	case len(m.projects) == 0:
		lines = append(lines, sgr("No projects yet", fgDim, "", false, m.color))
	case len(vis) == 0:
		lines = append(lines, sgr("Nothing matches", fgDim, "", false, m.color))
	}
	off := window(m.pCursor, m.pOff, len(vis), rows-2)
	for i := off; i < len(vis) && len(lines) < rows-2; i++ {
		p := m.projects[vis[i]]
		dot := sgr("● ", projectInk(p.Status), "", false, m.color)
		if p.Name == m.shown {
			lines = append(lines, dot+lit(sgr(fit(p.Name, inner-2), fgBright, "", true, m.color), inner-2, m.color))
		} else {
			lines = append(lines, dot+sgr(fit(p.Name, inner-2), fgSecondary, "", false, m.color))
		}
	}
	return box(title, lines, leftWidth, rows, focused, m.color)
}

// projectInk is a project's dot: what its production is doing.
func projectInk(status string) string {
	switch status {
	case "live":
		return pink
	case "building":
		return lavender
	case "failed":
		return coralTxt
	}
	return asleep
}

// addresses are the board's rows for the shown project: production first,
// then each branch's address.
func (m cockpit) addresses() []boardRow {
	var out []boardRow
	for _, r := range m.rows {
		if r.Project == m.shown {
			out = append(out, r)
		}
	}
	return out
}

// projectLayout shares the project pane's rows between its addresses and
// its deployments. Deployments get at least half when there are many of
// both, and addresses take what deployments don't need.
func (m cockpit) projectLayout() (addr, gap, dep int) {
	avail := m.paneRows() - 2
	n := max(1, len(m.addresses()))
	if avail >= 8 {
		gap = 1
	}
	room := max(0, avail-2-gap)
	addr = min(n, max(1, room/2))
	dep = room - addr
	if need := max(1, len(m.visibleDeployments())); dep > need && n > addr {
		extra := min(n-addr, dep-need)
		addr += extra
		dep -= extra
	}
	return addr, gap, max(0, dep)
}

func (m cockpit) deploymentRows() int {
	_, _, dep := m.projectLayout()
	return dep
}

func (m cockpit) projectPane(w, rows int) ([]string, []keyHelp) {
	inner := w - 4
	c := m.color
	focused := m.focused() == projectPane && m.dialog == noDialog
	keys := []keyHelp{{"d", "deploy"}, {"D", "production"}, {"p", "promote"}, {"r", "roll back"}, {"l", "logs"}, {"b", "board"}, {"/", "find"}, {"?", "help"}, {"q", "quit"}}
	if !m.wide() {
		keys = append(keys[:2], append([]keyHelp{{"tab", "projects"}}, keys[2:]...)...)
	}
	if m.shown == "" {
		lines := []string{sgr("No projects on "+m.server+" yet. Run pier deploy in a folder to make the first one.", fgSecondary, "", false, c)}
		return box("Nothing here yet", lines, w, rows, focused, c), keys
	}
	d := m.details[m.shown]
	addrRows, gap, depRows := m.projectLayout()

	var lines []string
	lines = append(lines, sgr("Addresses", fgMuted, "", false, c))
	addrs := m.addresses()
	idW, tileW := 9, tileWidth(8)
	addrW := inner - idW - tileW
	if addrW < 12 {
		idW = 0
		addrW = inner - tileW - 1
	}
	if len(addrs) == 0 {
		lines = append(lines, sgr("No addresses yet", fgDim, "", false, c))
	}
	shown := addrs
	hidden := 0
	if len(addrs) > addrRows {
		shown, hidden = addrs[:max(0, addrRows-1)], len(addrs)-max(0, addrRows-1)
	}
	for _, a := range shown {
		status, ink := a.Status, statusInk(a.Status)
		if d != nil && d.RolledBackFrom != "" && a.Kind == "production" && a.Status == "live" {
			status, ink = "rollback", pink
		}
		row := sgr(fit(a.Address, addrW), fgBright, "", false, c)
		if idW > 0 {
			row += sgr(fit(a.Deployment, idW), fgSecondary, "", false, c)
		} else {
			row += " "
		}
		lines = append(lines, row+tiles(status, ink, 8, c))
	}
	if hidden > 0 {
		lines = append(lines, sgr(fmt.Sprintf("%d more on the board, b shows them", hidden), fgDim, "", false, c))
	}
	for range gap {
		lines = append(lines, "")
	}
	label := "Deployments"
	if m.dFilter != "" {
		label = "Deployments matching " + m.dFilter
	}
	lines = append(lines, sgr(label, fgMuted, "", false, c))
	vis := m.visibleDeployments()
	switch {
	case d == nil:
		lines = append(lines, sgr("Loading "+m.shown+"…", fgDim, "", false, c))
	case len(d.Deployments) == 0:
		lines = append(lines, sgr("No deployments yet", fgDim, "", false, c))
	case len(vis) == 0:
		lines = append(lines, sgr("Nothing matches", fgDim, "", false, c))
	}
	off := window(m.dCursor, m.dOff, len(vis), depRows)
	for i := off; i < len(vis) && i < off+depRows; i++ {
		dep := d.Deployments[vis[i]]
		sel := focused && i == m.dCursor && m.view == homeView
		lines = append(lines, deploymentRow(dep, inner, sel, c))
	}
	title := m.shown
	return box(title, lines, w, rows, focused, c), keys
}

// deploymentWord is what a deployment is, in one word: live while
// production serves it, else whether it built.
func deploymentWord(d deploymentSummary) string {
	switch {
	case d.Role == "production":
		return "live"
	case d.Status == "building":
		return "building"
	case d.Status == "failed":
		return "failed"
	}
	return "ready"
}

func deploymentRow(d deploymentSummary, inner int, sel, c bool) string {
	word := deploymentWord(d)
	msg := cleanLog(strings.TrimSpace(d.Message))
	if msg == "" && d.Branch != "" {
		msg = "from " + cleanLog(d.Branch)
	}
	ageW := 12
	msgW := inner - 8 - 2 - ageW - 11
	if msgW < 10 {
		ageW = 0
		msgW = inner - 8 - 11
	}
	row := sgr(fit(d.ID, 6), fgSecondary, "", false, c) + "  "
	if msgW > 0 {
		row += sgr(fit(msg, msgW), fgBright, "", sel, c) + "  "
	}
	if ageW > 0 {
		row += sgr(fit(since(d.CreatedAt), ageW), fgDim, "", false, c)
	}
	row += dotted(word, statusInk(word), c)
	if sel {
		return lit(row, inner, c)
	}
	return row
}

// since says how long ago, as the cockpit's rows say it.
func since(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 2*time.Hour:
		return "an hour ago"
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hours ago", int(d.Hours()))
	case d < 48*time.Hour:
		return "yesterday"
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	}
	return t.Format("2 Jan")
}

// deployPane follows a deploy: its four steps with their numbers as
// tiles, a rule, and the tail of the build log under them.
func (m cockpit) deployPane(w, rows int) ([]string, []keyHelp) {
	r := m.run
	inner := w - 4
	c := m.color
	name := r.g.branch
	if name == "" {
		name = filepath.Base(r.dir)
	}
	title, edge := "Deploying "+name, lavender
	if r.target == "production" {
		title += " to production"
	}
	switch {
	case r.done && r.err != nil:
		title, edge = "The deploy of "+name+" failed", coralTxt
	case r.done:
		title, edge = "Deployed "+name, mint
		if r.target == "production" {
			title += " to production"
		}
	}

	var lines []string
	gap := rows-2 >= stepCount*2+6
	if gap {
		lines = append(lines, "")
	}
	textW := max(0, inner-25)
	for s := range stepCount {
		state := r.stepState(s)
		ink, mark, on := fgDim, " ", false
		switch state {
		case "done":
			ink, mark, on = mint, "✓", true
		case "running":
			ink, mark, on = lavender, spinnerFrames[m.spin], true
		case "failed":
			ink, mark, on = coralTxt, "✗", true
		}
		name, text := fgDim, fgDim
		if on {
			name, text = fgBright, fgSecondary
		}
		if state == "failed" {
			text = coralTxt
		}
		row := tiles(fmt.Sprintf("%02d", s+1), ink, 2, c) + "  " + sgr(fit(stepNames[s], 9), name, "", on, c) +
			sgr(fit(r.text[s], textW), text, "", false, c)
		if inner >= 30 {
			row += sgr(fmt.Sprintf("%6s", r.stepTime(s)), fgDim, "", false, c) + " " + sgr(mark, ink, "", false, c)
		}
		lines = append(lines, row)
		if gap {
			lines = append(lines, "")
		}
	}
	lines = append(lines, sgr(strings.Repeat("─", inner), fgBorder, "", false, c))
	room := rows - 2 - len(lines)
	tail := r.rows[max(0, len(r.rows)-max(0, room)):]
	if len(r.rows) == 0 && room > 0 {
		lines = append(lines, sgr("Packing "+filepath.Base(r.dir)+"…", fgDim, "", false, c))
	}
	for _, row := range tail {
		lines = append(lines, sgr(fit(row.label, 8), row.tone, "", false, c)+sgr(ansi.Truncate(row.message, max(0, inner-8), "…"), fgSecondary, "", false, c))
	}

	var keys []keyHelp
	switch {
	case !r.done:
		keys = []keyHelp{{"esc", "back; the build carries on"}, {"o", "open when live"}, {"l", "full log"}}
	case r.liveURL != "" && r.err == nil:
		keys = []keyHelp{{"o", "open " + host(r.liveURL)}, {"l", "full log"}, {"esc", "back"}}
	default:
		keys = []keyHelp{{"l", "full log"}, {"esc", "back"}}
	}
	return boxIn(title, lines, w, rows, edge, fgBright, c), keys
}

// detailPane is one deployment: what it is, where it came from, where it
// is served, and what can be done with it.
func (m cockpit) detailPane(w, rows int) ([]string, []keyHelp) {
	inner := w - 4
	c := m.color
	title := "Deployment " + m.detailID
	if m.detail == nil {
		return box(title, []string{sgr("Loading…", fgDim, "", false, c)}, w, rows, true, c), []keyHelp{{"esc", "back"}}
	}
	d := m.detail.Deployment
	labelW := 12
	valueW := max(0, inner-labelW)
	var lines []string
	field := func(label string, values ...string) {
		for i, v := range values {
			l := ""
			if i == 0 {
				l = label
			}
			lines = append(lines, sgr(fit(l, labelW), fgMuted, "", false, c)+pad(v, valueW))
		}
	}
	text := func(s, ink string) string { return sgr(ansi.Truncate(singleLine(s), valueW, "…"), ink, "", false, c) }
	word := deploymentWord(d)
	status := dotted(word, statusInk(word), c)
	if d.Framework != "" {
		kind := d.Framework
		if d.Runtime != "" {
			kind += ", a server"
		}
		status += sgr("   "+kind, fgSecondary, "", false, c)
	}
	field("Status", status)
	if msg := cleanLog(strings.TrimSpace(d.Message)); msg != "" {
		field("Message", text(msg, fgBright))
	}
	from := cleanLog(d.Branch)
	if d.Commit != "" {
		if from != "" {
			from += " at "
		}
		from += d.Commit[:min(7, len(d.Commit))]
	}
	if d.Source == "cli" {
		if from != "" {
			from += ", "
		}
		from += "sent by pier deploy"
	}
	if from != "" {
		field("From", text(from, fgSecondary))
	}
	var built []string
	if d.BuildMS > 0 {
		built = append(built, "in "+seconds(time.Duration(d.BuildMS)*time.Millisecond))
	}
	if !d.CreatedAt.IsZero() {
		built = append(built, since(d.CreatedAt))
	}
	if len(built) > 0 {
		label := "Built"
		if d.Status == "building" {
			label = "Started"
			built = built[len(built)-1:]
		}
		field(label, text(strings.Join(built, ", "), fgSecondary))
	}
	var addrs []string
	for _, a := range m.detail.Addresses {
		addrs = append(addrs, text(a.Host, fgBright))
	}
	if len(addrs) > 0 {
		field("Addresses", addrs...)
	}
	if st := m.detail.Container; st != nil {
		state := st.State
		if state == "oom_killed" {
			state = "oom killed"
		} else if state == "running" && st.Health == "unhealthy" {
			state = "unhealthy"
		}
		var parts []string
		if st.MemoryLimitMB > 0 {
			parts = append(parts, fmt.Sprintf("%d MB of memory", st.MemoryLimitMB))
		}
		if st.Restarts > 0 {
			parts = append(parts, plural(st.Restarts, "restart"))
		}
		if st.State == "running" && !st.StartedAt.IsZero() {
			parts = append(parts, "up since "+since(st.StartedAt))
		}
		v := dotted(state, statusInk(state), c)
		if len(parts) > 0 {
			v += sgr("   "+strings.Join(parts, ", "), fgSecondary, "", false, c)
		}
		field("Server", v)
	}
	if d.Status == "failed" && d.Error != "" {
		var wrapped []string
		for _, l := range strings.Split(ansi.Wrap(cleanLog(d.Error), max(1, valueW), ""), "\n") {
			wrapped = append(wrapped, sgr(l, coralTxt, "", false, c))
		}
		field("Why", wrapped...)
	}

	keys := []keyHelp{}
	if deploymentURL(m.detail) != "" {
		keys = append(keys, keyHelp{"o", "open"})
	}
	if d.Status == "ready" && d.Role != "production" {
		keys = append(keys, keyHelp{"p", "promote"})
	}
	if d.Role == "production" {
		keys = append(keys, keyHelp{"r", "roll back"})
	}
	keys = append(keys, keyHelp{"l", "build log"})
	if d.Runtime != "" {
		keys = append(keys, keyHelp{"s", "server log"})
		if d.Status == "ready" {
			keys = append(keys, keyHelp{"R", "restart"})
		}
	}
	if d.Status != "building" {
		keys = append(keys, keyHelp{"a", "build again"})
	}
	keys = append(keys, keyHelp{"esc", "back"})
	return box(title, lines, w, rows, true, c), keys
}

// boardPane is every address on the server, in one box the width of the
// terminal, with its status in tiles.
func (m cockpit) boardPane(w, rows int) []string {
	inner := w - 4
	c := m.color
	tileW := tileWidth(8)
	depW, branchW, addrW := 8, 8, 12
	for _, r := range m.rows {
		addrW = max(addrW, ansi.StringWidth(r.Address)+2)
		branchW = max(branchW, ansi.StringWidth(r.Branch)+2)
	}
	branchW = min(branchW, 18)
	if fixed := depW + branchW + tileW; addrW > inner-fixed {
		addrW = max(10, inner-fixed)
	}
	if addrW+depW+branchW+tileW > inner {
		branchW = 0
	}
	if addrW+depW+branchW+tileW > inner {
		depW = 0
		addrW = max(4, inner-tileW-1)
	}
	label := func(s string, n int) string { return sgr(fit(s, n), fgDim, "", false, c) }
	lines := []string{label("Address", addrW) + label("Deploy", depW) + label("Branch", branchW) + label("Status", tileW)}
	if len(m.rows) == 0 {
		lines = append(lines, sgr("Nothing on "+m.server+" yet. Deploy a folder to put it on the board.", fgDim, "", false, c))
	}
	visible := max(0, rows-3)
	off := window(m.bCursor, m.bOff, len(m.rows), visible)
	for i := off; i < len(m.rows) && i < off+visible; i++ {
		r := m.rows[i]
		sel := i == m.bCursor && m.dialog == noDialog
		row := sgr(fit(r.Address, addrW), fgBright, "", sel, c) + sgr(fit(r.Deployment, depW), fgSecondary, "", false, c) +
			sgr(fit(r.Branch, branchW), fgSecondary, "", false, c)
		if depW == 0 {
			row += " "
		}
		row += tiles(r.Status, statusInk(r.Status), 8, c)
		// The board marks its selection the way the design does, in bold;
		// without colour, where bold isn't drawn, the row is reversed.
		if sel && !c {
			row = lit(row, inner, c)
		}
		lines = append(lines, row)
	}
	return boxIn("Every address on "+m.server, lines, w, rows, fgBorder, fgSecondary, c)
}

// dialogBox is the dialog over the screen, and the keys it takes.
func (m cockpit) dialogBox() ([]string, []keyHelp) {
	c := m.color
	room := m.width - 6
	if m.wide() {
		room = m.width - leftWidth - 7
	}
	dw := min(64, room)
	if m.dialog == pickProject {
		longest := 0
		for _, it := range m.dlgItems {
			longest = max(longest, ansi.StringWidth(it.title))
		}
		dw = min(room, max(32, longest+10))
	}
	inner := dw - 4
	para := func(s string) []string {
		var out []string
		for _, l := range strings.Split(ansi.Wrap(s, max(1, inner), ""), "\n") {
			out = append(out, sgr(l, fgSecondary, "", false, c))
		}
		return out
	}
	list := func() []string {
		vis := m.dialogVisible()
		var out []string
		if len(vis) == 0 {
			out = append(out, sgr("Nothing matches", fgDim, "", false, c))
		}
		off := window(m.dlgCursor, m.dlgOff, len(vis), dialogListRows)
		for i := off; i < len(vis) && i < off+dialogListRows; i++ {
			it := m.dlgItems[vis[i]]
			var row string
			if m.dialog == pickProject {
				row = sgr("● ", it.ink, "", false, c) + sgr(fit(it.title, inner-2), fgBright, "", i == m.dlgCursor, c)
			} else {
				detailW := min(16, inner/3)
				row = sgr(fit(it.title, inner-detailW-1), fgBright, "", i == m.dlgCursor, c) + " " + sgr(fit(it.detail, detailW), fgDim, "", false, c)
			}
			if i == m.dlgCursor {
				row = lit(row, inner, c)
			}
			out = append(out, row)
		}
		return out
	}
	d := m.details[m.shown]
	address := m.shown + "'s address"
	if d != nil && d.Address != "" {
		address = d.Address
	}
	var title string
	var lines []string
	var keys []keyHelp
	switch m.dialog {
	case askProduction:
		title = "Deploy to production"
		lines = append(lines, "")
		lines = append(lines, para("Build "+filepath.Base(m.dir)+" and put it live at "+address+". The build carries on if you leave this screen.")...)
		keys = []keyHelp{{"enter", "deploy to production"}, {"esc", "cancel"}}
	case askPromote:
		id := m.dlgItems[0].id
		title = "Promote " + id
		msg := ""
		if d != nil {
			for _, dep := range d.Deployments {
				if dep.ID == id && strings.TrimSpace(dep.Message) != "" {
					msg = ", “" + cleanLog(strings.TrimSpace(dep.Message)) + "”"
				}
			}
		}
		lines = append(lines, "")
		lines = append(lines, para(address+" will serve "+id+msg+". What it serves now stays kept, so you can roll back to it.")...)
		keys = []keyHelp{{"enter", "promote"}, {"esc", "cancel"}}
	case pickRollback:
		title = "Roll back " + m.shown
		lines = append(lines, "")
		lines = append(lines, para(address+" goes back to the deployment you choose. New production builds wait until you promote one.")...)
		lines = append(lines, "")
		lines = append(lines, list()...)
		keys = []keyHelp{{"↑↓", "choose"}, {"enter", "roll back"}, {"esc", "cancel"}}
	case pickProject:
		title = "Projects"
		if m.dlgFilter != "" {
			title = "Projects matching " + m.dlgFilter
		}
		lines = list()
		keys = []keyHelp{{"↑↓", "move"}, {"enter", "show"}, {"type", "to find"}, {"esc", "close"}}
	}
	if m.dialog != pickProject {
		lines = append(lines, "")
	}
	// A cell of space around the dialog keeps what's under it from
	// touching its edges.
	rows := []string{strings.Repeat(" ", dw+2)}
	for _, r := range boxIn(title, lines, dw, len(lines)+2, blue, fgBright, c) {
		rows = append(rows, " "+r+" ")
	}
	return append(rows, strings.Repeat(" ", dw+2)), keys
}
