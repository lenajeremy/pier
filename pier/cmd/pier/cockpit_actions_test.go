package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// noClipboard makes the tests the same on every computer: no clipboard,
// so no Copy rows, unless a test says otherwise.
func noClipboard(t *testing.T) {
	saved := clipboard
	clipboard = func() []string { return nil }
	t.Cleanup(func() { clipboard = saved })
}

// detailOf opens the fixture's deployment id, the way Enter would once
// pierd answered.
func detailOf(t *testing.T, m cockpit, id string) cockpit {
	t.Helper()
	m.openDetail("landing", id, false)
	for _, d := range m.details["landing"].Deployments {
		if d.ID == id {
			m.detail = &deploymentDetail{Deployment: d}
		}
	}
	return m
}

func labels(acts []action) string {
	var out []string
	for _, a := range acts {
		out = append(out, a.label+" ("+a.key+")")
	}
	return strings.Join(out, ", ")
}

func TestDetailActionsPerKind(t *testing.T) {
	noClipboard(t)
	m := fixture(96, 30, true)
	ready := detailOf(t, m, "c19e77")
	ready.detail.Addresses = append(ready.detail.Addresses, struct{ Host, URL, Kind string }{"landing-c19e77.thesunset.live", "https://landing-c19e77.thesunset.live", "deployment"})
	live := detailOf(t, m, "8f3a2c")
	live.detail.Addresses = append(live.detail.Addresses, struct{ Host, URL, Kind string }{"landing.thesunset.live", "https://landing.thesunset.live", "production"})
	failed := detailOf(t, m, "5e0b71")
	building := detailOf(t, m, "c19e77")
	building.detail.Deployment.Status, building.detail.Deployment.Role = "building", "building"
	server := detailOf(t, m, "c19e77")
	server.detail.Deployment.Runtime = "node"
	for name, tc := range map[string]struct {
		m    cockpit
		want string
	}{
		"ready":    {ready, "Promote to production (p), Open landing-c19e77.thesunset.live (o), Build log (l), Build again (a), Back to landing (esc)"},
		"live":     {live, "Roll back production (r), Open landing.thesunset.live (o), Build log (l), Build again (a), Back to landing (esc)"},
		"failed":   {failed, "Build log (l), Build again (a), Back to landing (esc)"},
		"building": {building, "Build log (l), Back to landing (esc)"},
		"server":   {server, "Promote to production (p), Build log (l), Server log (s), Restart the server (R), Build again (a), Back to landing (esc)"},
	} {
		if got := labels(tc.m.detailActions()); got != tc.want {
			t.Errorf("%s:\n got %s\nwant %s", name, got, tc.want)
		}
	}
	clipboard = func() []string { return []string{"true"} }
	if got := labels(ready.detailActions()); !strings.Contains(got, "Copy the address (c)") {
		t.Errorf("with a clipboard the address can be copied: %s", got)
	}
	ready.detailFromRows = true
	if acts := ready.detailActions(); acts[len(acts)-1].label != "Back to the board" {
		t.Error("from the board, Back says it goes back to the board")
	}
}

func TestEnterRunsTheFocusedActionThroughItsDialog(t *testing.T) {
	noClipboard(t)
	m := detailOf(t, fixture(96, 30, true), "c19e77")
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "▌ Promote to production") {
		t.Fatalf("the first action should have the focus:\n%s", view)
	}
	m = press(t, m, "enter")
	if m.dialog != askPromote || m.dlgTarget != "c19e77" {
		t.Fatal("Enter on Promote should ask first, as p does")
	}
	if cancelled := press(t, m, "down", "enter"); cancelled.dialog != noDialog || cancelled.busy != "" {
		t.Fatal("Cancel should close the dialog and change nothing")
	}
	if promoted := press(t, m, "enter"); promoted.dialog != noDialog || !strings.Contains(promoted.busy, "Moving production to c19e77") {
		t.Fatalf("Enter on the dialog's first choice should promote, busy %q", promoted.busy)
	}

	live := detailOf(t, fixture(96, 30, true), "8f3a2c")
	if live.detailActions()[0].key != "r" {
		t.Fatal("the live deployment offers a rollback first")
	}
	live = press(t, live, "enter")
	if live.dialog != pickRollback || live.dlgItems[0].id != "1c9d22" || live.dlgItems[len(live.dlgItems)-1].id != "cancel" {
		t.Fatalf("Enter on Roll back should offer the one before first, then Cancel: %+v", live.dlgItems)
	}

	back := press(t, detailOf(t, fixture(96, 30, true), "c19e77"), "G", "enter")
	if back.view != homeView {
		t.Fatal("the last action goes back")
	}

	home := fixture(96, 30, true)
	home.focus = actionsPane
	if got := press(t, home, "enter"); got.run == nil || got.view != deployView {
		t.Fatal("Enter on Deploy this folder as a preview should start the deploy")
	}
	if got := press(t, home, "j", "enter"); got.dialog != askProduction {
		t.Fatal("Enter on Deploy this folder to production should ask first")
	}
	got := press(t, home, "j", "enter", "enter")
	if got.run == nil || got.run.target != "production" {
		t.Fatal("confirming should deploy to production")
	}
	got.run.cancel()
}

// cell is where text was drawn on a screen: its column and row.
func cell(t *testing.T, view, text string) (int, int) {
	t.Helper()
	for y, line := range strings.Split(view, "\n") {
		plain := ansi.Strip(line)
		if i := strings.Index(plain, text); i >= 0 {
			return ansi.StringWidth(plain[:i]), y
		}
	}
	t.Fatalf("%q isn't on the screen:\n%s", text, ansi.Strip(view))
	return 0, 0
}

func clickAt(t *testing.T, m cockpit, text string) cockpit {
	t.Helper()
	x, y := cell(t, m.View(), text)
	next, _ := m.Update(tea.MouseMsg{X: x + 1, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	return next.(cockpit)
}

func wheel(t *testing.T, m cockpit, text string, down bool) cockpit {
	t.Helper()
	x, y := cell(t, m.View(), text)
	b := tea.MouseButtonWheelUp
	if down {
		b = tea.MouseButtonWheelDown
	}
	next, _ := m.Update(tea.MouseMsg{X: x + 1, Y: y, Action: tea.MouseActionPress, Button: b})
	return next.(cockpit)
}

func TestClicksHitTheRowUnderThem(t *testing.T) {
	noClipboard(t)
	for _, size := range [][2]int{{96, 30}, {80, 24}} {
		m := fixture(size[0], size[1], true)

		dep := clickAt(t, m, "Fix the hero board")
		if dep.focused() != projectPane || dep.selectedDeployment().ID != "1c9d22" {
			t.Fatalf("%v: a click should select the deployment under it, got %+v", size, dep.selectedDeployment())
		}
		if opened := clickAt(t, dep, "Fix the hero board"); opened.view != detailView || opened.detailID != "1c9d22" {
			t.Fatalf("%v: a click on the selected deployment should open it", size)
		}

		act := clickAt(t, m, "Roll back production")
		if act.focused() != actionsPane || act.actKeys[homeView] != "r" || act.dialog != noDialog {
			t.Fatalf("%v: a click should focus the action, not run it yet", size)
		}
		act = clickAt(t, act, "Roll back production")
		if act.dialog != pickRollback {
			t.Fatalf("%v: a click on the focused action should run it", size)
		}
		chose := clickAt(t, act, "Cancel")
		if chose.dialog != pickRollback || chose.dlgItems[chose.dlgCursor].id != "cancel" {
			t.Fatalf("%v: a click should choose Cancel in the dialog", size)
		}
		if closed := clickAt(t, chose, "Cancel"); closed.dialog != noDialog {
			t.Fatalf("%v: a second click on Cancel should close the dialog", size)
		}

		det := detailOf(t, m, "c19e77")
		det = clickAt(t, det, "Build again")
		if det.actKeys[detailView] != "a" {
			t.Fatalf("%v: a click should focus Build again", size)
		}
		if help := clickAt(t, det, "? help"); help.view != helpView {
			t.Fatalf("%v: a click on a key in the key bar should press it", size)
		}

		board := press(t, m, "b")
		board = clickAt(t, board, "docs.thesunset.live")
		if board.rows[board.bCursor].Address != "docs.thesunset.live" {
			t.Fatalf("%v: a click should select the board row under it", size)
		}
		if opened := clickAt(t, board, "docs.thesunset.live"); opened.view != detailView || opened.detailID != "2b71e0" {
			t.Fatalf("%v: a click on the selected board row should open its deployment", size)
		}
	}

	wide := clickAt(t, fixture(96, 30, true), "docs")
	if wide.shown != "docs" || wide.focused() != projectsPane {
		t.Fatal("a click on a project should show it")
	}

	m := fixture(96, 30, true)
	x, y := cell(t, m.View(), "Fix the hero board")
	first, _ := m.Update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = first.(cockpit)
	m.focus = projectsPane // so the second click alone wouldn't open it
	second, _ := m.Update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if second.(cockpit).view != detailView {
		t.Fatal("a double click should open the deployment")
	}
}

func TestWheelScrollsTheListUnderIt(t *testing.T) {
	m := fixture(96, 30, true)
	m.focus = projectPane
	if got := wheel(t, m, "Keep the nav", true); got.dCursor != 1 {
		t.Fatalf("the wheel over the deployments should move them, at %d", got.dCursor)
	}
	if got := wheel(t, m, "agentman", true); got.focused() != projectsPane || got.shown != "agentman" {
		t.Fatalf("the wheel over the projects should move them, shows %s", got.shown)
	}
	board := press(t, fixture(80, 24, true), "b")
	if got := wheel(t, board, "docs.thesunset.live", true); got.bCursor != 1 {
		t.Fatalf("the wheel should move the board, at %d", got.bCursor)
	}
	det := detailOf(t, fixture(96, 30, true), "c19e77")
	if got := wheel(t, det, "Build log", true); got.actKeys[detailView] == "" || got.actKeys[detailView] == "p" {
		t.Fatal("the wheel should move through the actions")
	}
}

func TestActionRowsWithoutColour(t *testing.T) {
	noClipboard(t)
	m := detailOf(t, fixture(96, 30, false), "c19e77")
	view := m.View()
	if strings.Contains(view, "38;2;") || strings.Contains(view, "48;2;") {
		t.Fatal("NO_COLOR output has colour in it")
	}
	if !strings.Contains(view, "\x1b[7m▌ Promote to production") {
		t.Fatal("the focused action should be in reverse video")
	}
}

func TestPickersTakeClicks(t *testing.T) {
	menuAt := func(m menu, text string) menu {
		x, y := cell(t, m.View(), text)
		next, _ := m.Update(tea.MouseMsg{X: x + 1, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
		return next.(menu)
	}
	m := newMenu("Choose a project", "pier.example.com", "", []choice{{id: "landing", title: "landing"}, {id: "docs", title: "docs"}})
	m.width, m.height = 80, 24
	m = menuAt(m, "docs")
	if m.cursor != 1 || m.chosen != "" {
		t.Fatal("a click should select the item")
	}
	if m = menuAt(m, "docs"); m.chosen != "docs" {
		t.Fatal("a second click should choose it")
	}

	p := textPrompt{cfg: inputConfig{title: "Sign in to Pier"}, width: 80, height: 24}
	x, y := cell(t, p.View(), "Cancel")
	for range 2 {
		next, _ := p.Update(tea.MouseMsg{X: x + 1, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
		p = next.(textPrompt)
	}
	if !p.cancelled {
		t.Fatal("clicking Cancel twice should cancel the prompt")
	}

	noClipboard(t)
	lm := loginModel{base: "https://pier.example.com", link: "https://pier.example.com/approve", code: "BXRT-4KQ9", width: 80, height: 24}
	x, y = cell(t, lm.View(), "Cancel")
	for range 2 {
		next, _ := lm.Update(tea.MouseMsg{X: x + 1, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
		lm = next.(loginModel)
	}
	if lm.err != errCancelled {
		t.Fatal("clicking Cancel twice should give up signing in")
	}
}

// A viewer is offered only what looks. Pressing a key for something else
// says, in one sentence, why not and who can change it, and asks nothing.
func TestViewerIsOfferedOnlyWhatLooks(t *testing.T) {
	noClipboard(t)
	m := fixture(96, 30, true)
	m.role = "viewer"
	m.linked = "landing"
	for _, a := range m.homeActions() {
		if a.key == "d" || a.key == "D" || a.key == "r" {
			t.Errorf("a viewer is offered %q on the project", a.label)
		}
	}
	server := detailOf(t, m, "c19e77")
	server.detail.Deployment.Runtime = "node"
	if got, want := labels(server.detailActions()), "Build log (l), Server log (s), Back to landing (esc)"; got != want {
		t.Errorf("a viewer's deployment actions:\n got %s\nwant %s", got, want)
	}
	for key, want := range map[string]string{
		"d": "Viewers can't deploy. Ask the owner to make you a member.",
		"p": "Viewers can't promote deployments. Ask the owner to make you a member.",
		"r": "Viewers can't roll back production. Ask the owner to make you a member.",
	} {
		next, _ := m.key(keyMsg(key))
		got := next.(cockpit)
		if got.notice != want || got.dialog != noDialog || got.view != homeView {
			t.Errorf("%s as a viewer: notice %q, dialog %v, view %v", key, got.notice, got.dialog, got.view)
		}
	}
	for key, want := range map[string]string{
		"R": "Viewers can't restart a deployment. Ask the owner to make you a member.",
		"a": "Viewers can't redeploy. Ask the owner to make you a member.",
	} {
		next, _ := server.key(keyMsg(key))
		if got := next.(cockpit); got.notice != want || got.busy != "" {
			t.Errorf("%s on a deployment as a viewer: notice %q, busy %q", key, got.notice, got.busy)
		}
	}
	if view := ansi.Strip(m.View()); strings.Contains(view, "d deploy") || strings.Contains(view, "r roll back") {
		t.Errorf("a viewer's key bar offers changes:\n%s", view)
	}
}
