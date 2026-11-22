package main

import (
	"os/exec"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Every action a screen offers is also a row: something to move to and
// run with Enter, or click. The rows only show what applies right now, most
// important first, each with the key that does the same.

// homeActions are what the project pane offers: deploying this folder,
// when the project is this folder's, rolling production back when there's
// somewhere to go back to, and opening the production address. A viewer is
// offered only what looks, and told why if they press a key for the rest.
func (m cockpit) homeActions() []action {
	if m.shown == "" {
		return nil
	}
	var out []action
	switch {
	case m.run != nil && !m.run.done:
		out = append(out, action{"Show the deploy that's running", "d"})
	case m.linked != "" && m.linked == m.shown && !m.viewer():
		out = append(out, action{"Deploy this folder as a preview", "d"}, action{"Deploy this folder to production", "D"})
	}
	d := m.details[m.shown]
	if d == nil || !hasProduction(d) {
		return out
	}
	if len(rollbackChoices(d)) > 0 && !m.viewer() {
		out = append(out, action{"Roll back production", "r"})
	}
	if d.URL != "" {
		out = append(out, action{"Open " + d.Address, "o"})
	}
	return out
}

// detailActions are what can be done with the open deployment.
func (m cockpit) detailActions() []action {
	back := action{"Back to " + m.detailProject, "esc"}
	if m.detailFromRows {
		back.label = "Back to the board"
	}
	if m.detail == nil {
		return []action{back}
	}
	dep := m.detail.Deployment
	var out []action
	change := !m.viewer()
	if dep.Status == "ready" && dep.Role != "production" && change {
		out = append(out, action{"Promote to production", "p"})
	}
	if pd := m.details[m.detailProject]; dep.Role == "production" && pd != nil && len(rollbackChoices(pd)) > 0 && change {
		out = append(out, action{"Roll back production", "r"})
	}
	if u := deploymentURL(m.detail); u != "" {
		out = append(out, action{"Open " + host(u), "o"})
		if canCopy() {
			out = append(out, action{"Copy the address", "c"})
		}
	}
	out = append(out, action{"Build log", "l"})
	if dep.Runtime != "" {
		out = append(out, action{"Server log", "s"})
		if dep.Status == "ready" && change {
			out = append(out, action{"Restart the server", "R"})
		}
	}
	if dep.Status != "building" && change {
		out = append(out, action{"Build again", "a"})
	}
	return append(out, back)
}

// deployActions are what the deploy screen offers while it builds, and
// once it's live or has failed.
func (m cockpit) deployActions() []action {
	r := m.run
	switch {
	case !r.done:
		return []action{{"Back to the projects; the build carries on", "esc"}, {"Full build log", "l"}}
	case r.err == nil && r.liveURL != "":
		out := []action{{"Open " + host(r.liveURL), "o"}}
		if canCopy() {
			out = append(out, action{"Copy the address", "c"})
		}
		return append(out, action{"Build log", "l"}, action{"Back to the projects", "esc"})
	case r.err == nil:
		return []action{{"Build log", "l"}, {"Back to the projects", "esc"}}
	}
	return []action{{"Build log", "l"}, {"Deploy again", "d"}, {"Back to the projects", "esc"}}
}

// actions are the rows of the screen on show.
func (m cockpit) actions() []action {
	switch m.view {
	case homeView:
		return m.shownActions()
	case detailView:
		return m.detailActions()
	case deployView:
		if m.run != nil {
			return m.deployActions()
		}
	}
	return nil
}

// actionCursor is where the focused action is in a screen's list.
func (m cockpit) actionCursor(v view, acts []action) int {
	for i, a := range acts {
		if a.key == m.actKeys[v] {
			return i
		}
	}
	return 0
}

// moveAction moves the focus along a screen's actions. It reports false
// when the move would leave the list, so home can carry on into the pane
// above or below.
func (m *cockpit) moveAction(key string) bool {
	acts := m.actions()
	if len(acts) == 0 {
		return false
	}
	i := m.actionCursor(m.view, acts)
	switch key {
	case "up", "k":
		if i == 0 {
			return false
		}
		i--
	case "down", "j":
		if i == len(acts)-1 {
			return false
		}
		i++
	case "home", "g", "pgup":
		i = 0
	case "end", "G", "pgdown":
		i = len(acts) - 1
	}
	m.actKeys[m.view] = acts[i].key
	return true
}

// runAction does what the focused action says, the way its key does.
func (m cockpit) runAction() (tea.Model, tea.Cmd) {
	acts := m.actions()
	if len(acts) == 0 {
		return m, nil
	}
	return m.key(keyMsg(acts[m.actionCursor(m.view, acts)].key))
}

// keyMsg is the key press a key's name stands for, so a row or a click on
// the key bar can do exactly what the key does.
func keyMsg(key string) tea.KeyMsg {
	switch key {
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
}

func hasProduction(d *projectDetail) bool {
	for _, dep := range d.Deployments {
		if dep.Role == "production" {
			return true
		}
	}
	return false
}

// rollbackChoices are where production can go back to: an undo when it's
// rolled back already, then the deployment before it, then older ones.
func rollbackChoices(d *projectDetail) []dialogItem {
	var items, rest []dialogItem
	if d.RolledBackFrom != "" {
		items = append(items, dialogItem{id: "__undo__", title: "Undo the rollback", detail: "back to " + d.RolledBackFrom, ink: pink})
	}
	for _, dep := range d.Deployments {
		if dep.Status != "ready" || dep.Role == "production" || dep.Target != "production" {
			continue
		}
		item := dialogItem{id: dep.ID, title: dep.ID + "  " + cleanLog(dep.Message), detail: since(dep.CreatedAt), ink: mint}
		if dep.ID == d.RollbackTo {
			item.detail = "the one before"
			items = append(items, item)
			continue
		}
		rest = append(rest, item)
	}
	return append(items, rest...)
}

// doubleClick is how close two clicks on the same row have to be to count
// as one double click.
const doubleClick = 400 * time.Millisecond

// pressed notes a click and says whether it should run the row it hit:
// when the row was already the selected one, or the click is the second of
// a double click.
func pressed(last *click, h hit, selected bool) bool {
	now := time.Now()
	double := last.kind == h.kind && last.index == h.index && now.Sub(last.at) < doubleClick
	*last = click{kind: h.kind, index: h.index, at: now}
	return selected || double
}

// mouse answers clicks and the wheel. A click selects the row under it; a
// click on the row that's already selected, or a double click, runs it the
// way Enter would. The wheel moves through the list under the pointer.
func (m cockpit) mouse(e tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.view == logsView || e.Action != tea.MouseActionPress {
		return m, nil
	}
	if tea.MouseEvent(e).IsWheel() {
		return m.wheel(e)
	}
	if e.Button != tea.MouseButtonLeft || m.busy != "" {
		return m, nil
	}
	_, hits := m.render()
	h, ok := hitAt(hits, e.X, e.Y)
	if !ok {
		return m, nil
	}
	m.notice = ""
	now := time.Now()
	double := m.lastClick.kind == h.kind && m.lastClick.index == h.index && now.Sub(m.lastClick.at) < doubleClick
	m.lastClick = click{kind: h.kind, index: h.index, at: now}
	// Each kind of row knows whether it was already selected; a double
	// click runs it either way.
	switch h.kind {
	case hitKey:
		// "↑↓ move" and "type to find" describe keys rather than being
		// one, so there's nothing to run.
		if h.key == "↑↓" || h.key == "type" {
			return m, nil
		}
		return m.key(keyMsg(h.key))
	case hitChoice:
		again := m.dlgCursor == h.index
		m.dlgCursor = h.index
		m.scroll()
		if again || double {
			return m.dialogKey(keyMsg("enter"))
		}
	case hitProject:
		vis := m.visibleProjects()
		if h.index >= len(vis) {
			return m, nil
		}
		again := m.focused() == projectsPane && m.pCursor == h.index
		m.focus, m.pCursor = projectsPane, h.index
		cmd := m.show(m.projects[vis[h.index]].Name)
		if again || double {
			m.focus = projectPane
		}
		m.scroll()
		return m, cmd
	case hitDeployment:
		again := m.focused() == projectPane && m.dCursor == h.index
		m.focus, m.dCursor = projectPane, h.index
		m.scroll()
		if again || double {
			return m.key(keyMsg("enter"))
		}
	case hitAction:
		acts := m.actions()
		if h.index >= len(acts) {
			return m, nil
		}
		focused := m.view != homeView || m.focused() == actionsPane
		again := focused && m.actionCursor(m.view, acts) == h.index
		if m.view == homeView {
			m.focus = actionsPane
		}
		m.actKeys[m.view] = acts[h.index].key
		if again || double {
			return m.key(keyMsg(acts[h.index].key))
		}
	case hitBoard:
		again := m.bCursor == h.index
		m.bCursor = h.index
		m.scroll()
		if again || double {
			return m.key(keyMsg("enter"))
		}
	}
	return m, nil
}

// wheel moves through whichever list is under the pointer, a row at a time.
func (m cockpit) wheel(e tea.MouseMsg) (tea.Model, tea.Cmd) {
	key := "up"
	if e.Button == tea.MouseButtonWheelDown {
		key = "down"
	}
	switch {
	case e.Button != tea.MouseButtonWheelUp && e.Button != tea.MouseButtonWheelDown:
		return m, nil
	case m.dialog != noDialog:
		return m.dialogKey(keyMsg(key))
	case m.view == helpView:
		next, _ := m.help.Update(keyMsg(key))
		m.help = next.(helpModel)
		return m, nil
	case m.view == boardView:
		return m.boardKey(keyMsg(key))
	case m.view == homeView:
		if m.wide() && e.X < leftWidth {
			m.focus = projectsPane
		} else if m.focused() == projectsPane {
			m.focus = projectPane
		}
		return m, m.move(key)
	}
	m.moveAction(key)
	return m, nil
}

// clipboard is the command that puts text on this computer's clipboard,
// or nothing when it has none. Clicking turns the terminal's own selection
// off, so copying an address needs this instead.
var clipboard = sync.OnceValue(func() []string {
	for _, c := range [][]string{{"pbcopy"}, {"wl-copy"}, {"xclip", "-selection", "clipboard"}, {"xsel", "--clipboard", "--input"}} {
		if _, err := exec.LookPath(c[0]); err == nil {
			return c
		}
	}
	return nil
})

func canCopy() bool { return len(clipboard()) > 0 }

func copyText(s string) error {
	c := clipboard()
	cmd := exec.Command(c[0], c[1:]...)
	cmd.Stdin = strings.NewReader(s)
	return cmd.Run()
}

// copyAddress copies an address and says so.
func (m *cockpit) copyAddress(u string) {
	if u == "" || !canCopy() {
		return
	}
	if err := copyText(u); err != nil {
		m.say("Couldn't copy "+u+": "+err.Error(), coralTxt)
		return
	}
	m.say("✓ Copied "+u, mint)
}
