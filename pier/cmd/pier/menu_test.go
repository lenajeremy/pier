package main

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestMenuFilterAndSelect(t *testing.T) {
	m := newMenu("Choose a project", "pier.example.com", "", []choice{
		{id: "board", title: "Board", section: "Inspect"},
		{id: "logs", title: "Request logs", section: "Inspect"},
		{id: "runtime", title: "Runtime logs", section: "Inspect"},
	})
	key := func(k tea.KeyMsg) { next, _ := m.Update(k); m = next.(menu) }
	key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("logs"), Paste: true})
	if len(m.visible) != 2 {
		t.Fatalf("filter %q: %v", m.filter, m.visible)
	}
	key(tea.KeyMsg{Type: tea.KeyDown})
	key(tea.KeyMsg{Type: tea.KeyEnter})
	if m.chosen != "runtime" {
		t.Fatalf("selected %q", m.chosen)
	}
	key(tea.KeyMsg{Type: tea.KeyEsc})
	if m.search || len(m.visible) != 3 {
		t.Fatal("Escape did not clear filter")
	}
}

func TestMenuQuickJump(t *testing.T) {
	m := newMenu("Choose a project", "pier.example.com", "demo", []choice{
		{id: "landing", title: "landing"},
		{id: "docs", title: "docs"},
	})
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	if got := next.(menu); got.chosen != "docs" {
		t.Fatalf("quick jump chose %q, want docs", got.chosen)
	}
}

func TestBrandLayoutsAndMonochrome(t *testing.T) {
	mono := brand(80, false)
	if strings.Contains(mono, "\x1b") {
		t.Fatal("monochrome brand should not contain ANSI escape codes")
	}
	if !strings.Contains(mono, "deploy on your own server") {
		t.Fatal("wide brand missing tagline")
	}
	colored := brand(80, true)
	if !strings.Contains(colored, "\x1b[") {
		t.Fatal("colored brand should contain ANSI escape codes")
	}
	if ansi.Strip(colored) != mono {
		t.Fatalf("colored brand differs from monochrome once stripped:\n%s\n---\n%s", ansi.Strip(colored), mono)
	}
}

// checkScreen asserts what every full-screen view promises: exactly h
// rows, none wider than the terminal, the one-line header first and the
// keys last, and every box's rows as wide as its edges.
func checkScreen(t *testing.T, name, view string, w, h int, bar string) {
	t.Helper()
	lines := strings.Split(view, "\n")
	if len(lines) != h {
		t.Errorf("%s at %dx%d: %d rows", name, w, h, len(lines))
	}
	for i, l := range lines {
		if got := ansi.StringWidth(l); got > w {
			t.Errorf("%s at %dx%d: row %d is %d wide: %q", name, w, h, i, got, ansi.Strip(l))
		}
	}
	head := ansi.Strip(lines[0])
	if !strings.Contains(head, "▄█▄  pier") {
		t.Errorf("%s at %dx%d: no header: %q", name, w, h, head)
	}
	if strings.Contains(view, "█████▄") {
		t.Errorf("%s at %dx%d: the block-letter logo is back", name, w, h)
	}
	if bar != "" && w >= 60 && !strings.Contains(ansi.Strip(lines[len(lines)-1]), bar) {
		t.Errorf("%s at %dx%d: key bar %q missing from %q", name, w, h, bar, ansi.Strip(lines[len(lines)-1]))
	}
	if strings.Contains(ansi.Strip(view), "·") {
		t.Errorf("%s: a middle dot separator", name)
	}
}

var sizes = [][2]int{{96, 30}, {80, 24}, {120, 40}, {60, 16}, {40, 9}, {24, 6}, {12, 3}}

func TestMenuFitsTheTerminal(t *testing.T) {
	for _, size := range sizes {
		m := newMenu("Choose a project", "pier.thesunset.live", "landing", []choice{
			{id: "landing", title: "landing", badge: "live"},
			{id: "agentman", title: "agentman", badge: "building"},
			{id: "+", title: "Create a new project", hint: "new"},
		})
		m.width, m.height, m.color = size[0], size[1], true
		checkScreen(t, "menu", m.View(), size[0], size[1], "↑↓ move")
	}
}

func TestTextPromptEditAndValidate(t *testing.T) {
	m := textPrompt{
		cfg: inputConfig{
			title:       "Create a new project",
			server:      "pier.thesunset.live",
			label:       "Project name",
			hint:        "Lowercase letters, numbers, and single dashes",
			initial:     "website",
			placeholder: "website",
			validate: func(v string) error {
				if strings.Contains(v, "--") {
					return errCancelled
				}
				return nil
			},
		},
		value:  []rune("website"),
		cursor: len("website"),
		width:  84,
		height: 28,
		color:  true,
	}
	upd := func(k tea.KeyMsg) { next, _ := m.Update(k); m = next.(textPrompt) }
	upd(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("--bad")})
	upd(tea.KeyMsg{Type: tea.KeyEnter})
	if m.submitted || m.errMsg == "" {
		t.Fatalf("expected validation error, submitted=%v err=%q", m.submitted, m.errMsg)
	}
	for _, size := range sizes {
		m.width, m.height = size[0], size[1]
		checkScreen(t, "prompt", m.View(), size[0], size[1], "enter confirm")
	}
	upd(tea.KeyMsg{Type: tea.KeyCtrlU})
	upd(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("my-site")})
	upd(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.submitted || string(m.value) != "my-site" {
		t.Fatalf("got submitted=%v value=%q", m.submitted, string(m.value))
	}
}

func TestLoginAndHelpShareTheHeader(t *testing.T) {
	for _, size := range sizes {
		lm := loginModel{base: "https://pier.thesunset.live", link: "https://pier.thesunset.live/approve?code=BXRT-4KQ9", code: "BXRT-4KQ9", width: size[0], height: size[1], color: true}
		checkScreen(t, "login", lm.View(), size[0], size[1], "esc cancel")
		hm := helpModel{server: "pier.thesunset.live", project: "landing", width: size[0], height: size[1], color: true}
		checkScreen(t, "help", hm.View(), size[0], size[1], "esc back")
	}
	lm := loginModel{base: "https://pier.thesunset.live", code: "BXRT-4KQ9", width: 96, height: 30}
	if !strings.Contains(lm.View(), "BXRT-4KQ9") {
		t.Fatal("without colour the code should read as the word")
	}

	row, found := parseNarrationRow("alias   https://demo.thesunset.live", time.Now())
	if found != "https://demo.thesunset.live" || row.label != "alias" || row.tone != pink {
		t.Fatalf("parseNarrationRow = (%+v, %q)", row, found)
	}
}
