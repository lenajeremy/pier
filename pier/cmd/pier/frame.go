package main

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"pier/internal/version"
)

// The full-screen views are drawn cell for cell, the way the design was:
// every screen is a header on the first row, panes under it, and the keys
// on the last row. These helpers build those pieces as styled strings of an
// exact width, so a screen can be assembled line by line and never grow
// wider than the terminal.

// sgr paints s in one escape sequence. Without colour it adds nothing at
// all, not even bold, so NO_COLOR output is plain text.
func sgr(s, fg, bg string, bold, color bool) string {
	if !color || s == "" {
		return s
	}
	var codes []string
	if bold {
		codes = append(codes, "1")
	}
	if fg != "" {
		codes = append(codes, "38;2;"+fg)
	}
	if bg != "" {
		codes = append(codes, "48;2;"+bg)
	}
	if len(codes) == 0 {
		return s
	}
	return "\x1b[" + strings.Join(codes, ";") + "m" + s + "\x1b[0m"
}

// fit cuts plain text to n cells, ending in … when it had to, and pads it
// to exactly n, so columns line up whatever the text.
func fit(s string, n int) string {
	if n <= 0 {
		return ""
	}
	s = singleLine(s)
	if ansi.StringWidth(s) > n {
		s = ansi.Truncate(s, n, "…")
	}
	return s + strings.Repeat(" ", max(0, n-ansi.StringWidth(s)))
}

// pad cuts styled text to n cells and pads it to exactly n.
func pad(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if ansi.StringWidth(s) > n {
		s = ansi.Truncate(s, n, "…")
	}
	return s + strings.Repeat(" ", max(0, n-ansi.StringWidth(s)))
}

// lit puts the selected row's colour behind a whole row of n cells. The
// row's own pieces reset their style as they end, so the background is
// set again after every reset to stay unbroken. Without colour the row is
// shown in reverse instead, which isn't a colour, so a selection can still
// be seen.
func lit(s string, n int, color bool) string {
	body := pad(s, n)
	on := "\x1b[7m"
	if color {
		on = "\x1b[48;2;" + bgActive + "m"
	}
	return on + strings.ReplaceAll(body, "\x1b[0m", "\x1b[0m"+on) + "\x1b[0m"
}

// tiles draws a status the way the board shows it: each letter on its own
// tile, a cell of ground between letters, and blank tiles filling the
// column to n. Without colour it is the word in capitals.
func tiles(word, ink string, n int, color bool) string {
	letters := []rune(strings.ToUpper(word))
	if !color {
		return fit(string(letters), 2*n-1)
	}
	var b strings.Builder
	for i := range n {
		ch := " "
		if i < len(letters) {
			ch = string(letters[i])
		}
		b.WriteString(sgr(ch, ink, bgTile, true, true))
		if i < n-1 {
			b.WriteString(" ")
		}
	}
	return b.String()
}

// tileWidth is how many cells n tiles take.
func tileWidth(n int) int { return 2*n - 1 }

// statusInk is the colour a status is shown in, on the board's tiles and
// beside a deployment's dot.
func statusInk(status string) string {
	switch status {
	case "live", "production", "rollback":
		return pink
	case "preview", "building":
		return lavender
	case "ready", "healthy", "done", "running":
		return mint
	case "failed", "error", "unhealthy", "oom killed":
		return coralTxt
	case "asleep", "none", "exited", "stopped":
		return asleep
	case "restarting", "paused":
		return amber
	}
	return fgDim
}

// dotted is a coloured dot and the status in words, as deployment rows
// show what each one is.
func dotted(word, ink string, color bool) string {
	return sgr("● ", ink, "", false, color) + sgr(word, ink, "", false, color)
}

// boxTop is a pane's top edge with its title set into it. A focused pane
// draws its title and top edge in blue.
func boxTop(title string, w int, edge, titleInk string, color bool) string {
	if w < 2 {
		return ""
	}
	title = singleLine(title)
	if room := w - 6; room < 1 {
		return sgr("╭"+strings.Repeat("─", w-2)+"╮", edge, "", false, color)
	} else if ansi.StringWidth(title) > room {
		title = ansi.Truncate(title, room, "…")
	}
	fill := w - ansi.StringWidth(title) - 5
	return sgr("╭─", edge, "", false, color) + sgr(" "+title+" ", titleInk, "", true, color) +
		sgr(strings.Repeat("─", fill)+"╮", edge, "", false, color)
}

// boxRow is one row inside a pane: an edge, a cell of space, the content
// cut or padded to fit, a cell of space, an edge.
func boxRow(content string, w int, color bool) string {
	if w < 4 {
		return pad("", w)
	}
	edge := sgr("│", fgBorder, "", false, color)
	return edge + " " + pad(content, w-4) + " " + edge
}

func boxBottom(w int, color bool) string {
	if w < 2 {
		return ""
	}
	return sgr("╰"+strings.Repeat("─", w-2)+"╯", fgBorder, "", false, color)
}

// box draws a whole pane of h rows around rows of content, leaving the
// rest of it empty.
func box(title string, rows []string, w, h int, focused bool, color bool) []string {
	return boxIn(title, rows, w, h, edgeFor(focused), titleFor(focused), color)
}

func boxIn(title string, rows []string, w, h int, edge, titleInk string, color bool) []string {
	if h < 2 {
		return nil
	}
	out := []string{boxTop(title, w, edge, titleInk, color)}
	for i := range h - 2 {
		line := ""
		if i < len(rows) {
			line = rows[i]
		}
		out = append(out, boxRow(line, w, color))
	}
	return append(out, boxBottom(w, color))
}

func edgeFor(focused bool) string {
	if focused {
		return blue
	}
	return fgBorder
}

func titleFor(focused bool) string {
	if focused {
		return fgBright
	}
	return fgSecondary
}

// beside joins two panes into rows, a cell of space between them.
func beside(left, right []string, lw, rw int) []string {
	n := max(len(left), len(right))
	out := make([]string, 0, n)
	for i := range n {
		l, r := strings.Repeat(" ", lw), ""
		if i < len(left) {
			l = pad(left[i], lw)
		}
		if i < len(right) {
			r = right[i]
		}
		out = append(out, l+" "+pad(r, rw))
	}
	return out
}

// mark is Pier's mark in three cells: the dome's crown, middle and base.
func mark(color bool) string {
	return sgr("▄", pink, "", false, color) + sgr("█", rose, "", false, color) + sgr("▄", lavender, "", false, color)
}

// header is the first row of every screen: the mark, pier, where you are,
// and on the right this pier's release, or whatever needs saying there
// instead. Narrow terminals lose the right side first, then the server.
func header(w int, project, server, right, rightInk string, color bool) string {
	left := "  " + mark(color) + "  " + sgr("pier", fgBright, "", true, color) + "   "
	switch {
	case project != "" && server != "":
		left += sgr(singleLine(project), fgBright, "", true, color) + sgr(" on ", fgDim, "", false, color) + sgr(singleLine(server), fgSecondary, "", false, color)
	case project != "":
		left += sgr(singleLine(project), fgBright, "", true, color)
	case server != "":
		left += sgr(singleLine(server), fgSecondary, "", false, color)
	}
	if right == "" {
		right, rightInk = version.String(), fgDim
	}
	r := sgr(singleLine(right), rightInk, "", false, color)
	if gap := w - 2 - ansi.StringWidth(left) - ansi.StringWidth(r); gap >= 3 {
		return left + strings.Repeat(" ", gap) + r + "  "
	}
	return pad(left, w)
}

// keyHelp is one key and what it does, for the key bar.
type keyHelp struct{ key, does string }

// keyBar is the last row: each key and what it does, most important first,
// as many as fit.
func keyBar(keys []keyHelp, w int, color bool) string {
	line := " "
	for i, k := range keys {
		part := sgr(k.key, fgSecondary, "", true, color) + " " + sgr(k.does, fgDim, "", false, color)
		if i > 0 {
			part = "    " + part
		}
		if ansi.StringWidth(line)+ansi.StringWidth(part) > w {
			break
		}
		line += part
	}
	return line
}

// noticeBar says what just happened in the key bar's place, until the
// next key.
func noticeBar(text, ink string, w int, color bool) string {
	return " " + sgr(ansi.Truncate(singleLine(text), max(0, w-2), "…"), ink, "", false, color)
}

// screen puts a view together: the header, a blank row when there's room,
// the body, and the key bar on the last row. It is exactly h rows of at
// most w cells, whatever it was given.
func screen(head string, body []string, bar string, w, h int) string {
	lines := []string{head}
	if bodyRows(h) == h-3 {
		lines = append(lines, "")
	}
	room := bodyRows(h)
	for i := range room {
		if i < len(body) {
			lines = append(lines, body[i])
		} else {
			lines = append(lines, "")
		}
	}
	lines = append(lines, bar)
	lines = lines[:min(len(lines), max(1, h))]
	for i, l := range lines {
		if ansi.StringWidth(l) > w {
			lines[i] = ansi.Truncate(l, w, "")
		}
	}
	return strings.Join(lines, "\n")
}

// bodyRows is how many rows a screen of h rows leaves between its header
// and its key bar: a blank row under the header goes first when the
// terminal is short.
func bodyRows(h int) int {
	if h >= 10 {
		return h - 3
	}
	return max(0, h-2)
}

// window is the part of a list of n rows that fits in rows, starting at
// off and moved only as far as it takes to keep cursor in view.
func window(cursor, off, n, rows int) int {
	if rows <= 0 || n <= rows {
		return 0
	}
	off = min(max(off, 0), n-rows)
	if cursor < off {
		off = cursor
	}
	if cursor >= off+rows {
		off = cursor - rows + 1
	}
	return off
}

// overlay draws a dialog's rows over a screen's, starting at column x of
// row y. The rows under it keep their own styles on both sides.
func overlay(base, top []string, x, y, w int) []string {
	out := append([]string(nil), base...)
	for i, row := range top {
		at := y + i
		if at < 0 || at >= len(out) {
			continue
		}
		line := pad(out[at], max(w, x+ansi.StringWidth(row)))
		left := ansi.Truncate(line, x, "")
		right := ansi.TruncateLeft(line, x+ansi.StringWidth(row), "")
		out[at] = left + "\x1b[0m" + row + "\x1b[0m" + right
	}
	return out
}
