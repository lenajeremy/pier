package main

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"pier/internal/apphost"

	"pier/internal/requests"
)

const (
	logHistory = 1000
)

type logRow struct {
	stamp, clock, label, message, tone string
}

func cleanLog(s string) string { return singleLine(ansi.Strip(s)) }

func requestRow(l requests.Line) logRow {
	at := time.UnixMilli(l.At)
	tone := mint
	switch {
	case l.Status >= 500:
		tone = coral
	case l.Status >= 400:
		tone = amber
	case l.Status >= 300:
		tone = lavender
	}
	return logRow{at.Format(time.RFC3339), at.Format("15:04:05"), strconv.Itoa(l.Status),
		fmt.Sprintf("%s %q  %.1f ms  %s", cleanLog(l.Method), l.Path, l.MS, cleanLog(l.Deployment)), tone}
}

func runtimeRow(l apphost.LogLine) logRow {
	clock := cleanLog(l.At)
	if at, err := time.Parse(time.RFC3339Nano, l.At); err == nil {
		clock = at.Local().Format("15:04:05")
	}
	tone := mint
	if l.Stream == "stderr" {
		tone = coral
	}
	return logRow{cleanLog(l.At), clock, cleanLog(l.Stream), cleanLog(l.Text), tone}
}

type buildLine struct {
	MS        int64
	Tag, Text string
}

func buildRow(l buildLine) logRow {
	stamp := fmt.Sprintf("+%.1fs", float64(l.MS)/1000)
	tone := lavender
	if l.Tag == "build" {
		tone = mint
	}
	return logRow{stamp, stamp, cleanLog(l.Tag), cleanLog(l.Text), tone}
}

func (r logRow) format(styled, color bool) string {
	if !styled {
		line := fmt.Sprintf("%-6s  %s", r.label, r.message)
		if _, err := strconv.Atoi(r.label); err == nil {
			line = r.label + "  " + r.message
		}
		if r.stamp != "" {
			line = r.stamp + "  " + line
		}
		return line
	}
	return fmt.Sprintf("%s  %s  %s",
		ink(fmt.Sprintf("%8s", r.clock), fgDim, color),
		boldInk(fmt.Sprintf("%-6s", strings.ToUpper(r.label)), r.tone, color),
		ink(r.message, fgBright, color))
}

// Align continuation lines under the message when the terminal has room.
func (r logRow) wrapped(width int, color bool) []string {
	const gutter = 18
	if width < gutter+18 {
		return strings.Split(ansi.Hardwrap(r.format(true, color), width, false), "\n")
	}
	parts := strings.Split(ansi.Hardwrap(r.message, width-gutter, false), "\n")
	parts[0] = fmt.Sprintf("%s  %s  %s",
		ink(fmt.Sprintf("%8s", r.clock), fgDim, color),
		boldInk(fmt.Sprintf("%-6s", strings.ToUpper(r.label)), r.tone, color),
		ink(parts[0], fgBright, color))
	for i := 1; i < len(parts); i++ {
		parts[i] = strings.Repeat(" ", gutter) + ink(parts[i], fgBright, color)
	}
	return parts
}

type logBatch struct {
	rows []logRow
	err  error
	end  bool
}
type logView struct {
	source                                 logSource
	in                                     <-chan logBatch
	rows, frozen                           []logRow
	width, height, offset, unseen          int
	connected, paused, ended, retry, color bool
	err                                    error
}

func (m logView) readNext() tea.Cmd {
	return func() tea.Msg {
		b, ok := <-m.in
		if !ok {
			return logBatch{end: true}
		}
		// Drain a burst in one paint while still showing quiet streams immediately.
		for !b.end && len(b.rows) < 500 {
			select {
			case next, ok := <-m.in:
				if !ok {
					b.end = true
					return b
				}
				b.rows = append(b.rows, next.rows...)
				b.err, b.end = next.err, next.end
			default:
				return b
			}
		}
		return b
	}
}
func (m logView) Init() tea.Cmd { return m.readNext() }
func (m *logView) pause() {
	if !m.paused {
		m.frozen = slices.Clone(m.rows)
		m.paused = true
	}
}
func (m *logView) resume() { m.paused = false; m.frozen = nil; m.offset = 0; m.unseen = 0 }
func (m logView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = v.Width, v.Height
	case logBatch:
		m.connected = m.connected || v.err == nil || len(v.rows) > 0
		m.rows = append(m.rows, v.rows...)
		if len(m.rows) > logHistory {
			m.rows = m.rows[len(m.rows)-logHistory:]
		}
		if m.paused {
			m.unseen += len(v.rows)
		}
		if v.end {
			m.ended, m.err = true, v.err
			return m, nil
		}
		return m, m.readNext()
	case tea.KeyMsg:
		switch v.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "r":
			if m.ended {
				m.retry = true
				return m, tea.Quit
			}
		case " ":
			if m.paused {
				m.resume()
			} else {
				m.pause()
			}
		case "up", "k", "pgup":
			m.pause()
			step := 1
			if v.String() == "pgup" {
				step = m.bodyHeight()
			}
			_, available := m.body(m.offset + step)
			m.offset = available
		case "down", "j", "pgdown":
			step := 1
			if v.String() == "pgdown" {
				step = m.bodyHeight()
			}
			m.offset = max(0, m.offset-step)
		case "end", "G":
			m.resume()
		}
	}
	return m, nil
}

func (m logView) bodyHeight() int   { return max(1, m.height-7) }
func (m logView) contentWidth() int { return max(1, m.width-4) }

// Collect only the wrapped rows needed for this viewport, starting at the tail.
func (m logView) body(offset int) ([]string, int) {
	rows := m.rows
	if m.paused {
		rows = m.frozen
	}
	width, height := m.contentWidth(), m.bodyHeight()
	var lines []string
	for i := len(rows) - 1; i >= 0 && len(lines) < height+offset; i-- {
		parts := rows[i].wrapped(width, m.color)
		for j := len(parts) - 1; j >= 0; j-- {
			lines = append(lines, parts[j])
		}
	}
	offset = min(offset, max(0, len(lines)-height))
	lines = lines[offset:min(len(lines), offset+height)]
	slices.Reverse(lines)
	return lines, offset
}

func (m logView) View() string {
	w := m.contentWidth()
	c := m.color
	clip := func(s string) string { return "  " + ansi.Truncate(s, w, "…") }
	var out []string
	title := boldInk(m.source.kind+" logs", fgBright, c)
	context := boldInk(cleanLog(m.source.project), fgBright, c)
	if m.source.deployment != "" {
		context += " " + ink("/", fgDim, c) + " " + ink(cleanLog(m.source.deployment), fgMuted, c)
	}
	out = append(out, clip(miniMark(c)+"  "+boldInk("PIER", fgBright, c)+"  "+ink("/", fgDim, c)+"  "+context))
	state, tone := "Connecting", lavender
	switch {
	case m.err != nil:
		state, tone = "Disconnected", coralTxt
	case m.paused:
		state, tone = "Paused", amber
		if m.unseen > 0 {
			state += fmt.Sprintf(" · %d new", m.unseen)
		}
	case m.ended:
		state, tone = "Ended", lavender
	case m.connected:
		state, tone = "Live", mint
	}
	badge := ink("● ", tone, c) + boldInk(state, tone, c)
	gap := max(2, w-ansi.StringWidth(title)-ansi.StringWidth(badge))
	out = append(out, clip(title+strings.Repeat(" ", gap)+badge), clip(ink(strings.Repeat("─", w), fgBorder, c)))
	timeLabel, tagLabel, detailLabel := "TIME", "STATUS", "REQUEST"
	switch m.source.kind {
	case "Runtime":
		tagLabel, detailLabel = "STREAM", "OUTPUT"
	case "Build":
		timeLabel, tagLabel, detailLabel = "ELAPSED", "STAGE", "OUTPUT"
	}
	out = append(out, clip(ink(fmt.Sprintf("%8s  %-6s  %s", timeLabel, tagLabel, detailLabel), fgDim, c)))
	lines, _ := m.body(m.offset)
	if len(lines) == 0 {
		msg := "Opening log feed…"
		switch {
		case m.err != nil:
			msg = cleanLog(m.err.Error())
		case m.ended:
			msg = "No log lines."
		case m.connected:
			msg = "Waiting for the first line…"
		}
		lines = strings.Split(ansi.Hardwrap(msg, w, false), "\n")
		lines = lines[:min(len(lines), m.bodyHeight())]
		for i := range lines {
			lines[i] = ink(lines[i], fgMuted, c)
		}
	}
	for _, line := range lines {
		out = append(out, clip(line))
	}
	for len(lines) < m.bodyHeight() {
		out = append(out, "")
		lines = append(lines, "")
	}
	note := fmt.Sprintf("%d lines in history", len(m.rows))
	if len(m.rows) == 1 {
		note = "1 line in history"
	}
	noteTone := fgDim
	if m.err != nil {
		noteTone = coralTxt
		note = "Press r to reconnect"
		if len(m.rows) > 0 {
			note = cleanLog(m.err.Error())
		}
	}
	if m.paused && m.err == nil {
		noteTone = amber
		note = "Reading paused · collecting new lines"
		if m.ended {
			note = "Reading paused · stream ended"
		}
	}
	sep := ink("  ·  ", fgBorder, c)
	var hints []string
	if m.ended {
		hints = []string{keyHint("r", "reopen", c), keyHint("↑↓", "scroll", c), keyHint("esc", "back", c)}
	} else if m.paused {
		if w >= 52 {
			hints = []string{keyHint("space", "resume", c), keyHint("↑↓", "scroll", c), keyHint("G", "latest", c), keyHint("esc", "back", c)}
		} else {
			hints = []string{keyHint("space", "resume", c), keyHint("↑↓", "scroll", c), keyHint("esc", "back", c)}
		}
	} else {
		hints = []string{keyHint("space", "pause", c), keyHint("↑↓", "scroll", c), keyHint("esc", "back", c)}
	}
	if w < 44 {
		sep = "  "
	}
	out = append(out, clip(ink(strings.Repeat("─", w), fgBorder, c)), clip(ink(note, noteTone, c)), clip(strings.Join(hints, sep)))
	return strings.Join(out, "\n")
}

func openLogView(source logSource) error {
	for {
		ctx, cancel := context.WithCancel(context.Background())
		updates := make(chan logBatch, 16)
		go func() {
			defer close(updates)
			err := source.read(ctx, func(rows []logRow) error {
				select {
				case updates <- logBatch{rows: rows}:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})
			select {
			case updates <- logBatch{end: true, err: err}:
			case <-ctx.Done():
			}
		}()
		m := logView{source: source, in: updates, width: 80, height: 24, color: os.Getenv("NO_COLOR") == ""}
		final, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
		cancel()
		if err != nil {
			return err
		}
		if !final.(logView).retry {
			return nil
		}
	}
}
