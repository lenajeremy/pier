package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func (m cockpit) View() string {
	s, _ := m.render()
	return s
}

// render draws the screen and notes where each clickable thing landed:
// rows of every list, action rows, a dialog's choices, and the keys in the
// key bar. A click is matched against this, so it always hits what's
// drawn.
func (m cockpit) render() (string, []hit) {
	w, h := m.width, m.height
	top := bodyTop(h)
	rows := m.paneRows()
	var body []string
	var hits []hit
	var keys []keyHelp
	project := m.shown
	switch m.view {
	case logsView:
		// The mouse is off on the logs so their text can be selected.
		return m.logs.View(), nil
	case helpView:
		_, kh := keyBarHits(helpKeys, w, m.color)
		return m.help.View(), atRow(kh, h-1)
	case boardView:
		project = "every project"
		var bh []hit
		body, bh = m.boardPane(w, rows)
		hits = place(bh, 0, top)
		keys = []keyHelp{{"↑↓", "move"}, {"enter", "deployment"}, {"o", "open"}, {"l", "logs"}, {"esc", "back"}, {"?", "help"}}
	default:
		right, rh, k := m.rightPane()
		keys = k
		if m.wide() {
			left, lh := m.projectsPane(rows)
			body = beside(left, right, leftWidth, w-leftWidth-1)
			hits = append(place(lh, 0, top), place(rh, leftWidth+1, top)...)
		} else {
			body = right
			hits = place(rh, 0, top)
		}
	}
	bar, barHits := keyBarHits(keys, w, m.color)
	if m.searching {
		bar, barHits = m.searchBar(w), nil
	}
	switch {
	case m.busy != "":
		bar, barHits = noticeBar(spinnerFrames[m.spin]+" "+m.busy+"…", lavender, w, m.color), nil
	case m.notice != "":
		bar, barHits = noticeBar(m.notice, m.noticeInk, w, m.color), nil
	}
	if m.dialog != noDialog {
		rows, dh, dkeys, dw := m.dialogBox()
		bar, barHits = keyBarHits(dkeys, w, m.color)
		x := max(0, (w-dw-2)/2)
		if m.wide() {
			x = leftWidth + 1 + max(0, (w-leftWidth-1-dw-2)/2)
		}
		y := max(0, (len(body)-len(rows))/3)
		body = overlay(padRows(body, m.paneRows()), rows, x, y, w)
		// While a dialog is open, only its choices and keys answer.
		hits = place(dh, x+1, top+y+1)
	}
	hits = append(hits, atRow(barHits, h-1)...)
	right, rightInk := "", ""
	if m.offline != nil {
		right, rightInk = "can't reach "+m.server, coralTxt
	}
	return screen(header(w, project, m.server, right, rightInk, m.color), body, bar, w, h), hits
}

// atRow puts key bar hits on the screen row the bar is drawn on.
func atRow(hits []hit, y int) []hit {
	for i := range hits {
		hits[i].y = y
	}
	return hits
}

var helpKeys = []keyHelp{{"↑↓", "scroll"}, {"esc", "back"}}

func padRows(rows []string, n int) []string {
	for len(rows) < n {
		rows = append(rows, "")
	}
	return rows
}

// rightPane is whatever the screen shows beside the projects: the
// project, a deploy, or one deployment; with where its rows are and the
// keys that go with it.
func (m cockpit) rightPane() ([]string, []hit, []keyHelp) {
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
func (m cockpit) projectsPane(rows int) ([]string, []hit) {
	focused := m.focused() == projectsPane && m.view == homeView && m.dialog == noDialog
	title := "Projects"
	if m.pFilter != "" {
		title = "Projects matching " + m.pFilter
	}
	inner := leftWidth - 4
	vis := m.visibleProjects()
	var lines []string
	var hits []hit
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
		hits = append(hits, hit{y: len(lines), x1: leftWidth, kind: hitProject, index: i})
		if p.Name == m.shown {
			lines = append(lines, dot+lit(sgr(fit(p.Name, inner-2), fgBright, "", true, m.color), inner-2, m.color))
		} else {
			lines = append(lines, dot+sgr(fit(p.Name, inner-2), fgSecondary, "", false, m.color))
		}
	}
	return box(title, lines, leftWidth, rows, focused, m.color), hits
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

// projectLayout shares the project pane's rows between its addresses, its
// actions and its deployments. Actions show only when the pane is tall
// enough to keep a few deployments under them. Deployments get at least
// half of what's left when there are many of both, and addresses take what
// deployments don't need.
func (m cockpit) projectLayout() (addr, gap, acts, dep int) {
	avail := m.paneRows() - 2
	n := max(1, len(m.addresses()))
	if avail >= 8 {
		gap = 1
	}
	if a := len(m.homeActions()); a > 0 && avail-2-gap-1-(a+1+gap) >= 3 {
		acts = a
	}
	block := 0
	if acts > 0 {
		block = acts + 1 + gap
	}
	room := max(0, avail-2-gap-block)
	addr = min(n, max(1, room/2))
	dep = room - addr
	if need := max(1, len(m.visibleDeployments())); dep > need && n > addr {
		extra := min(n-addr, dep-need)
		addr += extra
		dep -= extra
	}
	return addr, gap, acts, max(0, dep)
}

func (m cockpit) deploymentRows() int {
	_, _, _, dep := m.projectLayout()
	return dep
}

// shownActions are the home actions the project pane has room to show.
func (m cockpit) shownActions() []action {
	if _, _, acts, _ := m.projectLayout(); acts > 0 {
		return m.homeActions()
	}
	return nil
}

func (m cockpit) projectPane(w, rows int) ([]string, []hit, []keyHelp) {
	inner := w - 4
	c := m.color
	active := m.dialog == noDialog
	focused := (m.focused() == projectPane || m.focused() == actionsPane) && active
	acts := m.shownActions()
	keys := []keyHelp{{"d", "deploy"}, {"D", "production"}, {"p", "promote"}, {"r", "roll back"}, {"l", "logs"}, {"b", "board"}, {"/", "find"}, {"?", "help"}, {"q", "quit"}}
	// Keys the pane already shows as rows leave the key bar.
	keys = withoutKeys(keys, acts)
	if !m.wide() {
		keys = append([]keyHelp{{"tab", "projects"}}, keys...)
	}
	if m.shown == "" {
		lines := []string{sgr("No projects on "+m.server+" yet. Run pier deploy in a folder to make the first one.", fgSecondary, "", false, c)}
		return box("Nothing here yet", lines, w, rows, focused, c), nil, keys
	}
	d := m.details[m.shown]
	addrRows, gap, _, depRows := m.projectLayout()

	var lines []string
	var hits []hit
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
	if len(acts) > 0 {
		for range gap {
			lines = append(lines, "")
		}
		lines = append(lines, sgr("Actions", fgMuted, "", false, c))
		cur := m.actionCursor(homeView, acts)
		for i, a := range acts {
			hits = append(hits, hit{y: len(lines), x1: w, kind: hitAction, index: i})
			lines = append(lines, actionRow(a, inner, active && m.focused() == actionsPane && i == cur, c))
		}
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
		sel := active && m.focused() == projectPane && i == m.dCursor && m.view == homeView
		hits = append(hits, hit{y: len(lines), x1: w, kind: hitDeployment, index: i})
		lines = append(lines, deploymentRow(dep, inner, sel, c))
	}
	return box(m.shown, lines, w, rows, focused, c), hits, keys
}

// withoutKeys drops from the key bar the keys that rows already show.
func withoutKeys(keys []keyHelp, acts []action) []keyHelp {
	var out []keyHelp
	for _, k := range keys {
		shown := false
		for _, a := range acts {
			if a.key == k.key {
				shown = true
			}
		}
		if !shown {
			out = append(out, k)
		}
	}
	return out
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
	// The age takes 12 ("23 hours ago") and always keeps two spaces before
	// the status, so the longest one never runs into its dot.
	ageW := 12
	msgW := inner - 8 - 2 - ageW - 2 - 11
	if msgW < 10 {
		ageW = 0
		msgW = inner - 8 - 11
	}
	row := sgr(fit(d.ID, 6), fgSecondary, "", false, c) + "  "
	if msgW > 0 {
		row += sgr(fit(msg, msgW), fgBright, "", sel, c) + "  "
	}
	if ageW > 0 {
		row += sgr(fit(since(d.CreatedAt), ageW), fgDim, "", false, c) + "  "
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
// tiles, what can be done now as rows, a rule, and the tail of the build
// log under them.
func (m cockpit) deployPane(w, rows int) ([]string, []hit, []keyHelp) {
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
	acts := m.deployActions()

	var lines []string
	var hits []hit
	gap := rows-2 >= stepCount*2+len(acts)+6
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
	if !gap {
		lines = append(lines, "")
	}
	cur := m.actionCursor(deployView, acts)
	for i, a := range acts {
		hits = append(hits, hit{y: len(lines), x1: w, kind: hitAction, index: i})
		lines = append(lines, actionRow(a, inner, m.dialog == noDialog && i == cur, c))
	}
	lines = append(lines, "", sgr(strings.Repeat("─", inner), fgBorder, "", false, c))
	room := rows - 2 - len(lines)
	tail := r.rows[max(0, len(r.rows)-max(0, room)):]
	if len(r.rows) == 0 && room > 0 {
		lines = append(lines, sgr("Packing "+filepath.Base(r.dir)+"…", fgDim, "", false, c))
	}
	for _, row := range tail {
		lines = append(lines, sgr(fit(row.label, 8), row.tone, "", false, c)+sgr(ansi.Truncate(row.message, max(0, inner-8), "…"), fgSecondary, "", false, c))
	}
	keys := []keyHelp{{"↑↓", "choose"}, {"enter", "run it"}, {"?", "help"}, {"q", "quit"}}
	return boxIn(title, lines, w, rows, edge, fgBright, c), hits, keys
}

// detailPane is one deployment: what it is, where it came from, where it
// is served, and what can be done with it, as rows to run.
func (m cockpit) detailPane(w, rows int) ([]string, []hit, []keyHelp) {
	inner := w - 4
	c := m.color
	title := "Deployment " + m.detailID
	keys := []keyHelp{{"↑↓", "choose"}, {"enter", "run it"}, {"?", "help"}, {"q", "quit"}}
	acts := m.detailActions()
	cur := m.actionCursor(detailView, acts)
	actionRows := func(lines []string) ([]string, []hit) {
		var hits []hit
		lines = append(lines, "", sgr("Actions", fgMuted, "", false, c))
		for i, a := range acts {
			hits = append(hits, hit{y: len(lines), x1: w, kind: hitAction, index: i})
			lines = append(lines, actionRow(a, inner, m.dialog == noDialog && i == cur, c))
		}
		return lines, hits
	}
	if m.detail == nil {
		lines, hits := actionRows([]string{sgr("Loading…", fgDim, "", false, c)})
		return box(title, lines, w, rows, true, c), hits, keys
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
		h := m.detail.Health
		if state == "oom_killed" {
			state = "oom killed"
		} else if state == "running" && (st.Health == "unhealthy" || h != nil && h.State == "unhealthy") {
			state = "unhealthy"
		} else if h != nil && h.State == "restarting" {
			state = "restarting"
		}
		var parts []string
		// Production's health checks: why it fails, or that it passes and
		// when it last didn't.
		if h != nil {
			switch {
			case h.State != "healthy" && h.LastFailure != "":
				why := "last check: " + h.LastFailure
				if h.State == "unhealthy" && !h.RestartedAt.IsZero() {
					why += ", restarted " + since(h.RestartedAt)
				}
				parts = append(parts, why)
			case h.State == "healthy":
				passing := "health check passing"
				if !h.LastFailureAt.IsZero() {
					passing += ", last failed " + since(h.LastFailureAt)
				}
				parts = append(parts, passing)
			}
		}
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
	// The actions always fit: a long error gives up its last lines first.
	if room := rows - 2 - (len(acts) + 2); len(lines) > room && room > 0 {
		lines = append(lines[:room-1], sgr(fit("…", labelW), fgMuted, "", false, c))
	}
	lines, hits := actionRows(lines)
	return box(title, lines, w, rows, true, c), hits, keys
}

// boardPane is every address on the server, in one box the width of the
// terminal, with its status in tiles.
func (m cockpit) boardPane(w, rows int) ([]string, []hit) {
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
	var hits []hit
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
		hits = append(hits, hit{y: len(lines), x1: w, kind: hitBoard, index: i})
		lines = append(lines, row)
	}
	return boxIn("Every address on "+m.server, lines, w, rows, fgBorder, fgSecondary, c), hits
}

// dialogBox is the dialog over the screen: its rows, where its choices
// are inside its box, the keys it takes, and how wide its box is.
func (m cockpit) dialogBox() ([]string, []hit, []keyHelp, int) {
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
		dw = min(room, max(32, longest+12))
	}
	dw = max(dw, 8)
	inner := dw - 4
	para := func(s string) []string {
		var out []string
		for _, l := range strings.Split(ansi.Wrap(s, max(1, inner), ""), "\n") {
			out = append(out, sgr(l, fgSecondary, "", false, c))
		}
		return out
	}
	var hits []hit
	choices := func(lines []string) []string {
		vis := m.dialogVisible()
		if len(vis) == 0 {
			lines = append(lines, sgr("Nothing matches", fgDim, "", false, c))
		}
		off := window(m.dlgCursor, m.dlgOff, len(vis), dialogListRows)
		for i := off; i < len(vis) && i < off+dialogListRows; i++ {
			hits = append(hits, hit{y: len(lines), x1: dw, kind: hitChoice, index: i})
			lines = append(lines, m.choiceRow(m.dlgItems[vis[i]], inner, i == m.dlgCursor))
		}
		return lines
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
		folder := filepath.Base(m.dir)
		if m.pending != nil {
			folder = filepath.Base(m.pending.dir)
		}
		lines = append(lines, "")
		lines = append(lines, para("Build "+folder+" and put it live at "+address+". The build carries on if you leave this screen.")...)
		lines = choices(append(lines, ""))
		keys = []keyHelp{{"↑↓", "choose"}, {"enter", "run it"}, {"esc", "cancel"}}
	case askPromote:
		id := m.dlgTarget
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
		lines = choices(append(lines, ""))
		keys = []keyHelp{{"↑↓", "choose"}, {"enter", "run it"}, {"esc", "cancel"}}
	case pickRollback:
		title = "Roll back " + m.shown
		lines = append(lines, "")
		lines = append(lines, para(address+" goes back to the deployment you choose. New production builds wait until you promote one.")...)
		lines = choices(append(lines, ""))
		keys = []keyHelp{{"↑↓", "choose"}, {"enter", "roll back"}, {"esc", "cancel"}}
	case pickProject:
		title = "Projects"
		if m.dlgFilter != "" {
			title = "Projects matching " + m.dlgFilter
		}
		lines = choices(nil)
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
	return append(rows, strings.Repeat(" ", dw+2)), hits, keys, dw
}

// choiceRow is one of a dialog's choices, drawn like an action row: a
// project with its dot, a deployment with how old it is, or a button with
// its key.
func (m cockpit) choiceRow(it dialogItem, inner int, focused bool) string {
	c := m.color
	mark, ink := "  ", fgSecondary
	if focused {
		mark, ink = sgr("▌ ", pink, "", false, c), fgBright
	}
	right := it.detail
	if right == "" {
		right = it.key
	}
	rightW := ansi.StringWidth(right)
	if it.detail != "" {
		rightW = max(rightW, min(16, inner/3))
	}
	row := mark
	titleW := inner - 2 - rightW - 2
	if m.dialog == pickProject {
		row += sgr("● ", it.ink, "", false, c)
		titleW -= 2
	}
	row += sgr(fit(it.title, max(0, titleW)), ink, "", focused, c)
	if right != "" {
		row += "  " + sgr(fit(right, rightW), fgDim, "", false, c)
	}
	if focused {
		return lit(row, inner, c)
	}
	return row
}
