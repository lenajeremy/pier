package main

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestMenuFilterAndSelect(t *testing.T) {
	m := newMenu("Command palette", "pier.example.com", "", []choice{
		{id: "board", title: "Board", icon: "◈", section: "Inspect"},
		{id: "logs", title: "Request logs", icon: "≡", section: "Inspect"},
		{id: "runtime", title: "Runtime logs", icon: "▸", section: "Inspect"},
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
	m := newMenu("Command palette", "pier.example.com", "demo", []choice{
		{id: "preview", title: "Deploy preview", section: "Deploy"},
		{id: "board", title: "Deployment board", section: "Inspect"},
	})
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	got := next.(menu)
	if got.chosen != "board" {
		t.Fatalf("quick jump chose %q, want board", got.chosen)
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
	if !strings.Contains(brand(32, false), "PIER") {
		t.Fatal("compact brand missing wordmark")
	}
	if !strings.Contains(brand(20, false), "PIER") {
		t.Fatal("mini brand missing wordmark")
	}
}

func TestMenuCardAlignment(t *testing.T) {
	m := newMenu("Command palette", "pier.thesunset.live", "lenajeremy-xyz", []choice{
		{id: "preview", title: "Deploy preview", hint: "pier deploy", icon: "▲", section: "Deploy"},
		{id: "prod", title: "Deploy production", hint: "pier deploy --prod", icon: "✦", section: "Deploy"},
		{id: "board", title: "Deployment board", hint: "pier board", icon: "◈", section: "Inspect"},
		{id: "history", title: "Deployment history", hint: "pier deployments", icon: "◷", section: "Inspect"},
		{id: "requests", title: "Request logs", hint: "live HTTP traffic", icon: "≡", section: "Inspect"},
		{id: "runtime", title: "Runtime logs", hint: "container stdout/stderr", icon: "▸", section: "Inspect"},
		{id: "build", title: "Build logs", hint: "latest build output", icon: "◇", section: "Inspect"},
		{id: "switch", title: "Switch project", hint: "pick active project", icon: "⇄", section: "Account"},
		{id: "help", title: "Command reference", hint: "pier help", icon: "?", section: "Account"},
		{id: "logout", title: "Sign out", hint: "pier logout", icon: "⇥", section: "Account"},
	})
	m.width, m.height, m.color = 84, 32, true
	view := m.View()
	stripped := ansi.Strip(view)
	t.Log("\n" + stripped)
	cardW := -1
	for _, line := range strings.Split(stripped, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "╭") || strings.HasPrefix(trimmed, "│") || strings.HasPrefix(trimmed, "├") || strings.HasPrefix(trimmed, "╰") {
			w := ansi.StringWidth(trimmed)
			if cardW < 0 {
				cardW = w
			} else if w != cardW {
				t.Fatalf("misaligned card row (width %d != %d): %q", w, cardW, trimmed)
			}
		}
	}
	if cardW <= 0 {
		t.Fatal("no card rendered")
	}
}

func TestDeploymentDetailCardAndRefresh(t *testing.T) {
	m := newMenu("Deployment 4e245b", "pier.thesunset.live", "lenajeremy-xyz", []choice{
		{id: "build", title: "Build logs", hint: "build output", icon: "◇", section: "Logs"},
		{id: "runtime", title: "Runtime logs", hint: "stdout / stderr", icon: "▸", section: "Logs"},
		{id: "requests", title: "Request logs", hint: "project HTTP traffic", icon: "≡", section: "Logs"},
		{id: "open", title: "Open in browser", hint: "lenajeremy.xyz", icon: "↗", section: "Actions"},
		{id: "restart", title: "Restart container", hint: "zero rebuild", icon: "↻", section: "Actions"},
		{id: "redeploy", title: "Redeploy", hint: "rebuild with latest settings", icon: "▲", section: "Actions"},
		{id: "rollback", title: "Roll back production", hint: "revert to previous", icon: "←", section: "Actions"},
	})
	m.meta = []metaRow{
		{label: "Status", value: statusPill("production", true) + ink("  ·  next 16.2 (server)", fgMuted, true), raw: true},
		{label: "Container", value: statusPill("running", true) + ink("  ·  1 GB RAM · up 3 hours ago", fgMuted, true), raw: true},
		{label: "URL", value: "https://lenajeremy.xyz", tone: fgBright},
		{label: "Source", value: "main · cb2e55a · Fix portfolio metadata", tone: fgSecondary},
		{label: "Built", value: "24.2s · 3 hours ago", tone: fgMuted},
	}
	m.refresh = true
	m.notice = "✓ Container restarted and passed readiness check"
	m.noticeTone = mint
	m.width, m.height, m.color = 84, 34, true

	stripped := ansi.Strip(m.View())
	t.Log("\n" + stripped)
	cardW := -1
	for _, line := range strings.Split(stripped, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "╭") || strings.HasPrefix(trimmed, "│") || strings.HasPrefix(trimmed, "├") || strings.HasPrefix(trimmed, "╰") {
			w := ansi.StringWidth(trimmed)
			if cardW < 0 {
				cardW = w
			} else if w != cardW {
				t.Fatalf("misaligned deployment detail card row (%d != %d): %q", w, cardW, trimmed)
			}
		}
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if next.(menu).chosen != refreshChoiceID {
		t.Fatalf("expected refresh on r key, got %q", next.(menu).chosen)
	}
}

func TestTextPromptEditValidateAndCardAlignment(t *testing.T) {
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
	// Type invalid double-dash, submit should stay open with error.
	upd := func(k tea.KeyMsg) { next, _ := m.Update(k); m = next.(textPrompt) }
	upd(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("--bad")})
	upd(tea.KeyMsg{Type: tea.KeyEnter})
	if m.submitted || m.errMsg == "" {
		t.Fatalf("expected validation error, submitted=%v err=%q", m.submitted, m.errMsg)
	}
	stripped := ansi.Strip(m.View())
	t.Log("\n" + stripped)
	cardW := -1
	for _, line := range strings.Split(stripped, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "╭") || strings.HasPrefix(trimmed, "│") || strings.HasPrefix(trimmed, "├") || strings.HasPrefix(trimmed, "╰") {
			w := ansi.StringWidth(trimmed)
			if cardW < 0 {
				cardW = w
			} else if w != cardW {
				t.Fatalf("misaligned prompt card row (%d != %d): %q", w, cardW, trimmed)
			}
		}
	}
	// Clear, type valid name, submit.
	upd(tea.KeyMsg{Type: tea.KeyCtrlU})
	upd(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("my-site")})
	upd(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.submitted || string(m.value) != "my-site" {
		t.Fatalf("got submitted=%v value=%q", m.submitted, string(m.value))
	}
}

func TestHelpAndDeployViewsRenderCleanly(t *testing.T) {
	hm := helpModel{server: "pier.thesunset.live", width: 84, height: 28, color: true}
	stripped := ansi.Strip(hm.View())
	cardW := -1
	for _, line := range strings.Split(stripped, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "╭") || strings.HasPrefix(trimmed, "│") || strings.HasPrefix(trimmed, "├") || strings.HasPrefix(trimmed, "╰") {
			w := ansi.StringWidth(trimmed)
			if cardW < 0 {
				cardW = w
			} else if w != cardW {
				t.Fatalf("misaligned help card row (%d != %d): %q", w, cardW, trimmed)
			}
		}
	}

	row, found := parseNarrationRow("alias   https://demo.thesunset.live", time.Now())
	if found != "https://demo.thesunset.live" || row.label != "alias" {
		t.Fatalf("parseNarrationRow = (%+v, %q)", row, found)
	}
}
