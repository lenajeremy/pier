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
	ink                                string // the message's colour, when it isn't the usual one
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
		fmt.Sprintf("%s %q  %.1f ms  %s", cleanLog(l.Method), l.Path, l.MS, cleanLog(l.Deployment)), tone, ""}
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
	return logRow{cleanLog(l.At), clock, cleanLog(l.Stream), cleanLog(l.Text), tone, ""}
}

type buildLine struct {
	MS        int64
	Tag, Text string
}

func buildRow(l buildLine) logRow {
	stamp := fmt.Sprintf("+%.1fs", float64(l.MS)/1000)
	return logRow{stamp, stamp, cleanLog(l.Tag), cleanLog(l.Text), narrationTone(cleanLog(l.Tag)), ""}
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

// wrapped is a row as the log viewer shows it: the time, the tag in its
// colour, and the message, with continuation lines under the message when
// the terminal has room.
func (r logRow) wrapped(width int, color bool) []string {
	const gutter = 18
	if width < gutter+18 {
		return strings.Split(ansi.Hardwrap(r.format(true, color), width, false), "\n")
	}
	ink := r.ink
	if ink == "" {
		ink = fgSecondary
	}
	parts := strings.Split(ansi.Hardwrap(r.message, width-gutter, false), "\n")
	parts[0] = sgr(fmt.Sprintf("%8s", r.clock), fgDim, "", false, color) + "  " +
		sgr(fmt.Sprintf("%-6s", r.label), r.tone, "", false, color) + "  " +
		sgr(parts[0], ink, "", false, color)
	for i := 1; i < len(parts); i++ {
		parts[i] = strings.Repeat(" ", gutter) + sgr(parts[i], ink, "", false, color)
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

// The log fills a box under the header: its edges and the column labels
// take three of the rows between the header and the key bar.
func (m logView) bodyHeight() int   { return max(1, bodyRows(m.height)-3) }
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
	state, tone := "connecting", lavender
	switch {
	case m.err != nil:
		state, tone = "disconnected", coralTxt
	case m.paused:
		state, tone = "paused", amber
		if m.unseen > 0 {
			state += fmt.Sprintf(", %d new", m.unseen)
		}
	case m.ended:
		state, tone = "ended", lavender
	case m.connected:
		state, tone = "live", mint
	}
	timeLabel, tagLabel, detailLabel := "Time", "Status", "Request"
	switch m.source.kind {
	case "Run":
		timeLabel, tagLabel, detailLabel = "Elapsed", "Stream", "Output"
	case "Runtime":
		tagLabel, detailLabel = "Stream", "Output"
	case "Build":
		timeLabel, tagLabel, detailLabel = "Elapsed", "Stage", "Output"
	}
	labels := sgr(fmt.Sprintf("%8s  %-6s  %s", timeLabel, tagLabel, detailLabel), fgDim, "", false, c)
	badge := dotted(state, tone, c)
	if gap := w - ansi.StringWidth(labels) - ansi.StringWidth(badge); gap >= 2 {
		labels += strings.Repeat(" ", gap) + badge
	}
	lines := []string{labels}
	body, _ := m.body(m.offset)
	if len(body) == 0 {
		msg := "Opening the log…"
		switch {
		case m.err != nil:
			msg = cleanLog(m.err.Error())
		case m.ended:
			msg = "No log lines."
		case m.connected:
			msg = "Waiting for the first line…"
		}
		for _, l := range strings.Split(ansi.Hardwrap(msg, w, false), "\n") {
			body = append(body, sgr(l, fgSecondary, "", false, c))
		}
		body = body[:min(len(body), m.bodyHeight())]
	}
	lines = append(lines, body...)

	note := plural(len(m.rows), "line") + " kept"
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
		note = "Paused here; new lines are still collected"
		if m.ended {
			note = "Paused here; the log has ended"
		}
	}
	var keys []keyHelp
	switch {
	case m.ended:
		keys = []keyHelp{{"r", "reopen"}, {"↑↓", "scroll"}, {"esc", "back"}}
	case m.paused:
		keys = []keyHelp{{"space", "resume"}, {"↑↓", "scroll"}, {"G", "latest"}, {"esc", "back"}}
	default:
		keys = []keyHelp{{"space", "pause"}, {"↑↓", "scroll"}, {"esc", "back"}}
	}
	bar := keyBar(keys, m.width, c)
	if n := sgr(note, noteTone, "", false, c); m.width-ansi.StringWidth(bar)-ansi.StringWidth(n)-2 >= 4 {
		bar += strings.Repeat(" ", m.width-ansi.StringWidth(bar)-ansi.StringWidth(n)-2) + n
	}
	head := header(m.width, cleanLog(m.source.project), host(m.source.c.Server), "", "", c)
	return screen(head, box(m.source.title(), lines, m.width, bodyRows(m.height), true, c), bar, m.width, m.height)
}

// title names what a log shows.
func (s logSource) title() string {
	of := cleanLog(s.deployment)
	switch s.kind {
	case "Run":
		if s.jobName == "" {
			return fmt.Sprintf("Run %d", s.run)
		}
		return fmt.Sprintf("Run %d of %s", s.run, cleanLog(s.jobName))
	case "Build":
		if of == "" {
			return "The latest build of " + cleanLog(s.project)
		}
		return "Build log of " + of
	case "Runtime":
		if of == "" {
			return "Server output of " + cleanLog(s.project)
		}
		return "Server output of " + of
	}
	return "Requests to " + cleanLog(s.project)
}

func openLogView(source logSource) error {
	for {
		updates, cancel := streamLogs(source)
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

// streamLogs reads a log in the background until it ends or is stopped,
// handing over its lines in batches.
func streamLogs(source logSource) (<-chan logBatch, context.CancelFunc) {
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
	return updates, cancel
}
