package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"pier/internal/board"
)

// fixture is a cockpit as it opens on the mock's server, without asking
// one: four projects, landing's addresses and deployments, and the board.
func fixture(w, h int, color bool) cockpit {
	ago := func(d time.Duration) time.Time { return time.Now().Add(-d) }
	row := func(addr, project, kind, dep, branch, status string) boardRow {
		return boardRow{Row: board.Row{Address: addr, Project: project, Kind: kind, Deployment: dep, Branch: branch, Status: status}, URL: "https://" + addr}
	}
	m := cockpit{
		server: "pier.thesunset.live", width: w, height: h, color: color,
		linked: "landing", dir: "/Users/someone/code/landing",
		details: map[string]*projectDetail{},
		projects: []cockpitProject{
			{Name: "landing", Status: "live"}, {Name: "agentman", Status: "building"},
			{Name: "docs", Status: "live"}, {Name: "status", Status: "failed"},
		},
		rows: []boardRow{
			row("agentman.thesunset.live", "agentman", "production", "3d9c04", "main", "live"),
			row("docs.thesunset.live", "docs", "production", "2b71e0", "main", "live"),
			row("landing.thesunset.live", "landing", "production", "8f3a2c", "main", "live"),
			row("feat-install--landing.thesunset.live", "landing", "branch", "c19e77", "feat-install", "preview"),
			row("fix-mobile-nav--landing.thesunset.live", "landing", "branch", "41d0e9", "fix-mobile-nav", "building"),
		},
	}
	m.details["landing"] = &projectDetail{
		Name: "landing", Address: "landing.thesunset.live", URL: "https://landing.thesunset.live", Status: "live", RollbackTo: "1c9d22",
		Deployments: []deploymentSummary{
			{ID: "41d0e9", Message: "Keep the nav on one line on phones", Status: "ready", Role: "preview", Target: "preview", CreatedAt: ago(26 * time.Hour)},
			{ID: "c19e77", Message: "Show the install command on phones", Status: "ready", Role: "kept", Target: "preview", CreatedAt: ago(3 * time.Minute)},
			{ID: "8f3a2c", Message: "Add the install command to the hero", Status: "ready", Role: "production", Target: "production", CreatedAt: ago(2 * time.Hour)},
			{ID: "5e0b71", Message: "Swap the waitlist for install", Status: "failed", Role: "failed", Target: "production", CreatedAt: ago(2 * time.Hour)},
			{ID: "1c9d22", Message: "Fix the hero board on Safari", Status: "ready", Role: "kept", Target: "production", CreatedAt: ago(72 * time.Hour)},
		},
	}
	m.choose("landing")
	return m
}

func press(t *testing.T, m cockpit, keys ...string) cockpit {
	t.Helper()
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case "up":
			msg = tea.KeyMsg{Type: tea.KeyUp}
		case "backspace":
			msg = tea.KeyMsg{Type: tea.KeyBackspace}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		next, _ := m.Update(msg)
		m = next.(cockpit)
	}
	return m
}

// running is a deploy halfway: pushed, and building.
func running(m *cockpit) {
	m.startDeployState(pendingDeploy{project: "landing", dir: m.dir, target: "preview", g: git{branch: "feat-install", commit: "c19e77aa"}})
	r := m.run
	for _, line := range []string{
		"pier    received 214 files from pier deploy, for preview",
		"build   vite 8.3, found in package.json",
		"build   running in a throwaway node:22 container",
		"build   added 182 packages in 6.1s",
		"build   vite v8.3.8 building for production…",
	} {
		row, u := parseNarrationRow(line, r.started)
		m.onDeploy(deployEvent{row: &row, url: u})
	}
}

func TestCockpitScreensFitEverySize(t *testing.T) {
	for _, size := range sizes {
		w, h := size[0], size[1]
		for _, color := range []bool{true, false} {
			m := fixture(w, h, color)
			checkScreen(t, "home", m.View(), w, h, "d deploy")
			focused := m
			focused.focus = projectPane
			checkScreen(t, "home, the project focused", focused.View(), w, h, "d deploy")
			checkScreen(t, "board", press(t, m, "b").View(), w, h, "↑↓ move")
			checkScreen(t, "help", press(t, m, "?").View(), w, h, "esc back")
			checkScreen(t, "promote", press(t, focused, "down", "p").View(), w, h, "enter promote")
			checkScreen(t, "roll back", press(t, m, "r").View(), w, h, "")
			checkScreen(t, "production", press(t, m, "D").View(), w, h, "enter deploy")

			d := m
			running(&d)
			checkScreen(t, "deploy", d.View(), w, h, "esc back")

			det := press(t, focused, "down")
			det.view, det.detailProject, det.detailID = detailView, "landing", "c19e77"
			det.detail = &deploymentDetail{Deployment: det.details["landing"].Deployments[1]}
			checkScreen(t, "detail", det.View(), w, h, "esc back")

			lg := m
			lg.view, lg.logOpen = logsView, true
			lg.logs = logView{source: logSource{project: "landing", kind: "Build", deployment: "c19e77"}, width: w, height: h, color: color}
			lg.logs.rows = []logRow{buildRow(buildLine{MS: 1200, Tag: "build", Text: strings.Repeat("a long line of output ", 10)})}
			checkScreen(t, "logs", lg.View(), w, h, "esc back")
		}
	}
}

func TestCockpitMatchesTheDesign(t *testing.T) {
	m := fixture(96, 30, true)
	m.focus = projectPane
	m.dCursor = 1
	lines := strings.Split(ansi.Strip(m.View()), "\n")
	if !strings.HasPrefix(lines[0], "  ▄█▄  pier   landing on pier.thesunset.live") {
		t.Fatalf("header %q", lines[0])
	}
	if lines[1] != "" || !strings.HasPrefix(lines[2], "╭─ Projects ─") || !strings.Contains(lines[2], "╮ ╭─ landing ─") {
		t.Fatalf("panes %q %q", lines[1], lines[2])
	}
	if got := ansi.StringWidth(strings.SplitAfter(lines[2], "╮")[0]); got != leftWidth {
		t.Fatalf("projects pane is %d wide", got)
	}
	for _, want := range []string{"Addresses", "landing.thesunset.live", "L I V E", "P R E V I E W", "Deployments", "c19e77  Show the install command on pho…", "● ready", "● live", "● failed"} {
		if !strings.Contains(strings.Join(lines, "\n"), want) {
			t.Errorf("home lacks %q", want)
		}
	}
	if !strings.HasPrefix(lines[29], " d deploy    D production    p promote    r roll back    l logs    b board    / find    ? help") {
		t.Fatalf("keys %q", lines[29])
	}
	colored := m.View()
	if !strings.Contains(colored, "\x1b[38;2;"+blue+"m╭─") {
		t.Fatal("the focused pane's top edge should be blue")
	}
	if !strings.Contains(colored, "\x1b[48;2;"+bgActive+"m") {
		t.Fatal("the selected row should have the row colour")
	}
}

func TestCockpitWithoutColour(t *testing.T) {
	for _, v := range []cockpit{fixture(96, 30, false), press(t, fixture(96, 30, false), "b")} {
		view := v.View()
		if strings.Contains(view, "38;2;") || strings.Contains(view, "48;2;") {
			t.Fatal("NO_COLOR output has colour in it")
		}
		for _, word := range []string{"LIVE", "PREVIEW", "BUILDING"} {
			if !strings.Contains(view, word) {
				t.Errorf("a tile without colour should be the word %s", word)
			}
		}
	}
}

func TestCockpitNarrowHidesTheProjects(t *testing.T) {
	if strings.Contains(ansi.Strip(fixture(60, 16, true).View()), "fix-mobile-nav--landing.thesunset.live") {
		t.Fatal("long addresses should be cut with … on narrow terminals")
	}
	m := fixture(80, 24, true)
	view := ansi.Strip(m.View())
	if strings.Contains(view, "Projects") {
		t.Fatal("below 90 columns the projects pane should be hidden")
	}
	if !strings.Contains(strings.Split(view, "\n")[0], "landing") {
		t.Fatal("the header should still name the project")
	}
	m = press(t, m, "tab")
	if m.dialog != pickProject {
		t.Fatal("tab should open the project picker when the pane is hidden")
	}
	m = press(t, m, "d", "o", "c", "enter")
	if m.shown != "docs" || m.dialog != noDialog {
		t.Fatalf("the picker chose %q", m.shown)
	}
}

func TestCockpitKeys(t *testing.T) {
	m := fixture(96, 30, true)
	if m.focus != projectsPane {
		t.Fatal("the cockpit starts on the projects")
	}
	m = press(t, m, "j")
	if m.shown != "agentman" {
		t.Fatalf("moving the cursor shows %q", m.shown)
	}
	m = press(t, m, "k", "tab", "j", "j")
	if m.focus != projectPane || m.selectedDeployment().ID != "8f3a2c" {
		t.Fatalf("focus %v, deployment %+v", m.focus, m.selectedDeployment())
	}
	if got := press(t, m, "p"); got.notice == "" || got.dialog != noDialog {
		t.Fatal("promoting production should say it already is")
	}
	if got := press(t, m, "k", "p"); got.dialog != askPromote || got.dlgItems[0].id != "c19e77" {
		t.Fatal("p should ask before promoting the selected deployment")
	}
	if got := press(t, m, "r"); got.dialog != pickRollback || got.dlgItems[0].id != "1c9d22" {
		t.Fatalf("r should offer the deployment before first, got %+v", got.dlgItems)
	}
	if got := press(t, m, "enter"); got.view != detailView || got.detailID != "8f3a2c" {
		t.Fatal("enter on a deployment should open it")
	}
	if got := press(t, m, "/", "w", "a", "i"); len(got.visibleDeployments()) != 1 || !got.searching {
		t.Fatal("/ should find deployments in the focused pane")
	}
	if got := press(t, m, "/", "w", "a", "i", "enter", "esc"); got.dFilter != "" || got.searching {
		t.Fatal("esc should clear the filter")
	}
	other := press(t, fixture(96, 30, true), "j", "d")
	if other.run != nil || !strings.Contains(other.notice, "This folder deploys to landing") {
		t.Fatalf("d on another project should say so, got %q", other.notice)
	}
	if got := press(t, fixture(96, 30, true), "D"); got.dialog != askProduction {
		t.Fatal("D should ask before deploying to production")
	}
	board := press(t, fixture(96, 30, true), "b", "j", "j")
	if board.view != boardView || board.rows[board.bCursor].Address != "landing.thesunset.live" {
		t.Fatal("b should open the board, and ↑↓ move on it")
	}
	if press(t, board, "esc").view != homeView {
		t.Fatal("esc should go back from the board")
	}
}

func TestCockpitKeepsSelectionOnRefresh(t *testing.T) {
	m := press(t, fixture(96, 30, true), "tab", "j", "j")
	d := *m.details["landing"]
	d.Deployments = append([]deploymentSummary{{ID: "a1b2c3", Status: "building", Role: "building"}}, d.Deployments...)
	projects := append([]cockpitProject{{Name: "aardvark", Status: "none"}}, m.projects...)
	next, _ := m.Update(cockpitData{projects: projects, rows: m.rows, name: "landing", detail: &d})
	m = next.(cockpit)
	if got := m.selectedDeployment().ID; got != "8f3a2c" {
		t.Fatalf("the selection moved to %s", got)
	}
	if m.projects[m.visibleProjects()[m.pCursor]].Name != "landing" {
		t.Fatal("the project cursor moved")
	}
	if !m.active() {
		t.Fatal("a build should make the cockpit refresh every few seconds")
	}
}

func TestDeploySteps(t *testing.T) {
	m := fixture(96, 30, true)
	running(&m)
	r := m.run
	if r.stepState(stepPush) != "done" || r.stepState(stepBuild) != "running" || r.stepState(stepDeploy) != "waiting" {
		t.Fatalf("steps %v %v %v", r.stepState(0), r.stepState(1), r.stepState(2))
	}
	if r.text[stepPush] != "received c19e77a from this folder" || r.text[stepBuild] != "vite 8.3 in a throwaway node:22 container" {
		t.Fatalf("texts %q %q", r.text[stepPush], r.text[stepBuild])
	}
	view := ansi.Strip(m.View())
	for _, want := range []string{"Deploying feat-install", "0 1  Push", "0 2  Build", "Go live", "build   vite v8.3.8 building for production…"} {
		if !strings.Contains(view, want) {
			t.Errorf("deploy screen lacks %q\n%s", want, view)
		}
	}
	for _, line := range []string{
		"deploy  kept as c19e77: 214 files, 1.3 MB, never changed again",
		"alias   https://landing-c19e77.thesunset.live",
		"pier    preview ready in 9.9s",
	} {
		row, u := parseNarrationRow(line, r.started)
		m.onDeploy(deployEvent{row: &row, url: u})
	}
	m.onDeploy(deployEvent{done: true})
	for s := range stepCount {
		if r.stepState(s) != "done" {
			t.Fatalf("step %d is %s", s, r.stepState(s))
		}
	}
	if r.liveURL != "https://landing-c19e77.thesunset.live" || r.text[stepLive] != "live at landing-c19e77.thesunset.live" {
		t.Fatalf("live %q %q", r.liveURL, r.text[stepLive])
	}

	f := fixture(96, 30, true)
	running(&f)
	row, _ := parseNarrationRow("pier    build failed after 3.0s: npm run build failed: exit status 1", f.run.started)
	f.onDeploy(deployEvent{row: &row})
	f.onDeploy(deployEvent{done: true, err: fmt.Errorf("the build failed; nothing changed")})
	if f.run.stepState(stepBuild) != "failed" || f.run.stepState(stepDeploy) != "waiting" {
		t.Fatalf("failure: build %s, deploy %s", f.run.stepState(stepBuild), f.run.stepState(stepDeploy))
	}
	if !strings.Contains(ansi.Strip(f.View()), "npm run build failed") {
		t.Fatal("the log should show why it failed")
	}
}
