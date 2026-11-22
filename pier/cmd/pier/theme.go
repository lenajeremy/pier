package main

import (
	"fmt"
	"os"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	terminal "golang.org/x/term"

	"pier/internal/version"
)

// Pier's terminal palette: the dashboard's dark tokens, from
// pier/web/src/styles/tokens.css.
const (
	fgBright    = "236;238;244" // #ECEEF4 text
	fgSecondary = "163;171;190" // #A3ABBE muted: secondary text, keys
	fgMuted     = "138;146;168" // #8A92A8 note: section labels, hints
	fgDim       = "94;102;123"  // #5E667B faint: ages, waiting steps, key labels
	fgBorder    = "38;45;62"    // #262D3E hairlines drawn with box characters

	bgActive = "20;26;40" // #141A28 the selected row
	bgTile   = "22;28;43" // #161C2B a tile: one letter per cell

	pink     = "244;156;203" // #F49CCB production, live
	rose     = "218;164;225" // #DAA4E1 the mark's middle
	lavender = "188;174;248" // #BCAEF8 preview, building
	mint     = "98;216;198"  // #62D8C6 healthy, ready, done
	amber    = "240;184;92"  // #F0B85C warnings, paused
	coral    = "255;128;128" // #FF8080 failed, stderr
	coralTxt = "255;180;180" // #FFB4B4 failed text
	blue     = "141;178;248" // #8DB2F8 pierd, and the focused pane
	asleep   = "142;150;170" // #8E96AA nothing deployed
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
	release := version.String()
	var lines []string
	for i, row := range markLines {
		left := ink(row, markTones[i], color)
		if width >= 48 {
			right := wordmarkLines[i]
			if i < 4 {
				right = boldInk(right, fgBright, color)
				lines = append(lines, "  "+left+"   "+right)
				continue
			}
			// The release sits on the tagline's row, ending where the
			// commands in the card below end.
			tag := strings.TrimSpace(right)
			line := "  " + left + "   " + ink(tag, fgMuted, color)
			edge := min(width, 80)
			if gap := edge - ansi.StringWidth(line) - len(release); gap >= 3 {
				line += strings.Repeat(" ", gap) + ink(release, fgDim, color)
			}
			lines = append(lines, line)
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

func colorEnabled() bool {
	return os.Getenv("NO_COLOR") == "" && term()
}

func statusTone(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "live", "production":
		return pink
	case "preview", "building", "branch":
		return lavender
	case "ready", "healthy", "done", "running", "succeeded":
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
