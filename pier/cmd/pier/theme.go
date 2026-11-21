package main

import (
	"fmt"
	"os"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	terminal "golang.org/x/term"
)

// Pier's terminal palette, taken directly from pier/web/src/styles/tokens.css.
const (
	fgBright    = "236;238;244" // #ECEEF4 primary ink
	fgSecondary = "163;171;190" // #A3ABBE secondary text
	fgMuted     = "138;146;168" // #8A92A8 hints, metadata
	fgDim       = "94;102;123"  // #5E667B reflection, timestamps, dim labels
	fgBorder    = "54;62;82"    // #363E52 hairline card borders & rules

	bgActive = "22;28;44" // #161C2C selected row fill

	pink     = "244;156;203" // #F49CCB production / live / dome crown
	rose     = "218;164;225" // #DAA4E1 sunset dome mid-tone
	lavender = "188;174;248" // #BCAEF8 preview / building / dome base
	mint     = "98;216;198"  // #62D8C6 ready / healthy / done
	amber    = "240;184;92"  // #F0B85C warnings / paused
	coral    = "255;128;128" // #FF8080 failed / stderr
	coralTxt = "255;180;180" // #FFB4B4 failed text
)

// The rising half-dome, horizon line, and shorter water reflection match
// Pier's SVG mark in pier/web/src/components/Icons.tsx.
var markLines = []string{
	"     ▄▄▄▄▄▄     ",
	"   ▄████████▄   ",
	"  ████████████  ",
	"━━━━━━━━━━━━━━━━",
	"    ━━━━━━━━    ",
}

// Crisp geometric half-block wordmark aligned to cap-height (row 0) and horizon
// baseline (row 3), with the quiet tagline aligned to the reflection (row 4).
var wordmarkLines = []string{
	"█████▄  ██  ██████  █████▄",
	"██  ██  ██  ██▄▄    ██  ██",
	"█████▀  ██  ██▀▀    █████▀",
	"██      ██  ██████  ██  ██",
	"deploy on your own server ",
}

var markTones = []string{pink, rose, lavender, fgBright, fgDim}

func brand(width int, color bool) string {
	if width <= 0 {
		width = 72
	}
	if width < 24 {
		return "  " + ink("▄█▄", pink, color) + " " + boldInk("PIER", fgBright, color)
	}
	var lines []string
	for i, row := range markLines {
		left := ink(row, markTones[i], color)
		if width >= 48 {
			right := wordmarkLines[i]
			if i < 4 {
				right = boldInk(right, fgBright, color)
			} else {
				right = ink(strings.TrimSpace(right), fgMuted, color)
			}
			lines = append(lines, "  "+left+"   "+right)
		} else {
			if i == 2 {
				lines = append(lines, "  "+left+"  "+boldInk("PIER", fgBright, color))
			} else {
				lines = append(lines, "  "+left)
			}
		}
	}
	return strings.Join(lines, "\n")
}

func miniMark(color bool) string {
	return ink("▄", pink, color) + ink("█", rose, color) + ink("▄", lavender, color)
}

func ink(s, rgb string, enabled bool) string {
	if !enabled || rgb == "" || s == "" {
		return s
	}
	return "\x1b[38;2;" + rgb + "m" + s + "\x1b[0m"
}

func boldInk(s, rgb string, enabled bool) string {
	if !enabled || s == "" {
		return s
	}
	if rgb == "" {
		return "\x1b[1m" + s + "\x1b[0m"
	}
	return "\x1b[1;38;2;" + rgb + "m" + s + "\x1b[0m"
}

func bgLine(s, bg string, width int, enabled bool) string {
	pad := max(0, width-ansi.StringWidth(s))
	full := s + strings.Repeat(" ", pad)
	if !enabled || bg == "" {
		return full
	}
	// Re-apply the row background after any inner ANSI reset so the highlight bar stays unbroken.
	prefix := "\x1b[48;2;" + bg + "m"
	body := strings.ReplaceAll(full, "\x1b[0m", "\x1b[0m"+prefix)
	return prefix + body + "\x1b[0m"
}

func colorEnabled() bool {
	return os.Getenv("NO_COLOR") == "" && term()
}

func statusTone(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "live", "production":
		return pink
	case "preview", "building", "branch":
		return lavender
	case "ready", "healthy", "done", "running":
		return mint
	case "failed", "error", "oom_killed", "unhealthy":
		return coralTxt
	case "paused", "restarting", "warning":
		return amber
	default:
		return fgMuted
	}
}

func statusPill(status string, color bool) string {
	clean := strings.TrimSpace(status)
	if clean == "" {
		return ""
	}
	tone := statusTone(clean)
	word := strings.ToUpper(clean)
	return ink("● ", tone, color) + boldInk(word, tone, color)
}

func keyHint(key, label string, color bool) string {
	return boldInk(key, fgSecondary, color) + " " + ink(label, fgDim, color)
}

func terminalWidth() int {
	w, _, err := terminal.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 {
		return 74
	}
	return max(20, min(w-4, 82))
}

func singleLine(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}

func heading(title, detail string) {
	width := terminalWidth()
	c := colorEnabled()
	fmt.Println()
	top := miniMark(c) + "  " + boldInk("PIER", fgBright, c) + "  " + ink("/", fgDim, c) + "  " + boldInk(title, fgBright, c)
	if detail != "" {
		det := ink(singleLine(detail), fgMuted, c)
		gap := width - ansi.StringWidth(top) - ansi.StringWidth(det)
		if gap >= 2 {
			top += strings.Repeat(" ", gap) + det
		} else {
			fmt.Println("  " + ansi.Truncate(top, width, "…"))
			fmt.Println("  " + ansi.Truncate(det, width, "…"))
			fmt.Println("  " + ink(strings.Repeat("─", width), fgBorder, c))
			fmt.Println()
			return
		}
	}
	fmt.Println("  " + ansi.Truncate(top, width, "…"))
	fmt.Println("  " + ink(strings.Repeat("─", width), fgBorder, c))
	fmt.Println()
}

func resultRow(label, status, detail string) {
	width := terminalWidth()
	c := colorEnabled()
	tag := statusPill(status, c)
	available := width - ansi.StringWidth(tag) - 3
	if available < 6 {
		fmt.Println("  " + ansi.Truncate(boldInk(singleLine(label), fgBright, c), width, "…"))
		if tag != "" {
			fmt.Println("  " + ansi.Truncate(tag, width, "…"))
		}
	} else {
		left := ansi.Truncate(singleLine(label), available, "…")
		styled := boldInk(left, fgBright, c)
		gap := max(2, width-ansi.StringWidth(styled)-ansi.StringWidth(tag))
		fmt.Println("  " + styled + strings.Repeat(" ", gap) + tag)
	}
	if detail != "" {
		fmt.Println("  " + ansi.Truncate(ink(singleLine(detail), fgMuted, c), width, "…"))
	}
	fmt.Println()
}

// cardRow formats one line inside a rounded border box of total inner width `inner`.
func cardRow(content string, inner int, color bool) string {
	clipped := ansi.Truncate(content, inner, "…")
	pad := max(0, inner-ansi.StringWidth(clipped))
	border := ink("│", fgBorder, color)
	return "  " + border + " " + clipped + strings.Repeat(" ", pad) + " " + border
}

func cardTop(inner int, color bool) string {
	return "  " + ink("╭"+strings.Repeat("─", inner+2)+"╮", fgBorder, color)
}

func cardDivider(inner int, color bool) string {
	return "  " + ink("├"+strings.Repeat("─", inner+2)+"┤", fgBorder, color)
}

func cardBottom(inner int, color bool) string {
	return "  " + ink("╰"+strings.Repeat("─", inner+2)+"╯", fgBorder, color)
}
