package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"pier/internal/bundle"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// ---- 1. Reusable TUI text input card ----------------------------------------

type inputConfig struct {
	title       string
	server      string
	project     string
	label       string
	hint        string
	initial     string
	placeholder string
	validate    func(string) error
}

type textPrompt struct {
	cfg           inputConfig
	value         []rune
	cursor        int
	errMsg        string
	width, height int
	color         bool
	submitted     bool
	cancelled     bool
}

func (m textPrompt) Init() tea.Cmd { return nil }

func (m textPrompt) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch k := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = k.Width, k.Height
	case tea.KeyMsg:
		switch k.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			m.cancelled = true
			return m, tea.Quit
		case tea.KeyEnter:
			v := strings.TrimSpace(string(m.value))
			if v == "" && m.cfg.placeholder != "" && m.cfg.initial != "" {
				v = m.cfg.initial
			}
			if m.cfg.validate != nil {
				if err := m.cfg.validate(v); err != nil {
					m.errMsg = err.Error()
					return m, nil
				}
			}
			m.value = []rune(v)
			m.submitted = true
			return m, tea.Quit
		case tea.KeyLeft:
			if m.cursor > 0 {
				m.cursor--
			}
		case tea.KeyRight:
			if m.cursor < len(m.value) {
				m.cursor++
			}
		case tea.KeyHome, tea.KeyCtrlA:
			m.cursor = 0
		case tea.KeyEnd, tea.KeyCtrlE:
			m.cursor = len(m.value)
		case tea.KeyCtrlU:
			m.value = nil
			m.cursor = 0
			m.errMsg = ""
		case tea.KeyBackspace:
			if m.cursor > 0 {
				m.value = append(m.value[:m.cursor-1], m.value[m.cursor:]...)
				m.cursor--
				m.errMsg = ""
			}
		case tea.KeyDelete:
			if m.cursor < len(m.value) {
				m.value = append(m.value[:m.cursor], m.value[m.cursor+1:]...)
				m.errMsg = ""
			}
		case tea.KeyRunes, tea.KeySpace:
			var text string
			if k.Type == tea.KeySpace {
				text = " "
			} else {
				text = string(k.Runes)
			}
			clean := []rune(singleLine(text))
			if len(clean) > 0 {
				next := make([]rune, 0, len(m.value)+len(clean))
				next = append(next, m.value[:m.cursor]...)
				next = append(next, clean...)
				next = append(next, m.value[m.cursor:]...)
				m.value = next
				m.cursor += len(clean)
				m.errMsg = ""
			}
		}
	}
	return m, nil
}

func (m textPrompt) View() string {
	inner := max(26, min(72, m.width-8))
	c := m.color
	var out []string
	if m.height >= 22 && inner >= 46 {
		out = append(out, "", brand(inner+4, c), "")
	} else {
		out = append(out, "", "  "+miniMark(c)+"  "+boldInk("PIER", fgBright, c))
	}

	out = append(out, cardTop(inner, c))
	headLeft := boldInk(m.cfg.title, fgBright, c)
	headRight := ""
	if m.cfg.server != "" {
		headRight = ink("● ", mint, c) + ink(m.cfg.server, fgMuted, c)
		if m.cfg.project != "" {
			headRight += "  " + ink("◆ ", pink, c) + boldInk(m.cfg.project, fgSecondary, c)
		}
	}
	if headRight != "" && inner-ansi.StringWidth(headLeft)-ansi.StringWidth(headRight) >= 2 {
		gap := inner - ansi.StringWidth(headLeft) - ansi.StringWidth(headRight)
		out = append(out, cardRow(headLeft+strings.Repeat(" ", gap)+headRight, inner, c))
	} else {
		out = append(out, cardRow(headLeft, inner, c))
	}
	out = append(out, cardDivider(inner, c))
	out = append(out, cardRow("", inner, c))
	if m.cfg.label != "" {
		out = append(out, cardRow("  "+ink(m.cfg.label, fgMuted, c), inner, c))
	}

	// Highlighted input well with visible pink caret.
	var field string
	if len(m.value) == 0 && m.cfg.placeholder != "" {
		field = ink("▌ ", pink, c) + ink("▏", pink, c) + ink(m.cfg.placeholder, fgDim, c)
	} else {
		before := string(m.value[:m.cursor])
		after := string(m.value[m.cursor:])
		field = ink("▌ ", pink, c) + boldInk(before, fgBright, c) + ink("▏", pink, c) + boldInk(after, fgBright, c)
	}
	out = append(out, cardRow(bgLine(field, bgActive, inner, c), inner, c))
	out = append(out, cardRow("", inner, c))

	if m.errMsg != "" {
		out = append(out, cardRow("  "+ink(singleLine(m.errMsg), coralTxt, c), inner, c))
	} else if m.cfg.hint != "" {
		out = append(out, cardRow("  "+ink(singleLine(m.cfg.hint), fgDim, c), inner, c))
	}

	out = append(out, cardBottom(inner, c))
	sep := "    "
	hints := strings.Join([]string{
		keyHint("enter", "confirm", c),
		keyHint("ctrl+u", "clear", c),
		keyHint("esc", "back", c),
	}, sep)
	out = append(out, "  "+ansi.Truncate(hints, inner+4, "…"))
	return strings.Join(out, "\n")
}

func promptInput(cfg inputConfig) (string, bool, error) {
	initRunes := []rune(cfg.initial)
	m := textPrompt{
		cfg:    cfg,
		value:  initRunes,
		cursor: len(initRunes),
		width:  80,
		height: 26,
		color:  os.Getenv("NO_COLOR") == "",
	}
	final, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	if err != nil {
		return "", false, err
	}
	res := final.(textPrompt)
	if res.cancelled || !res.submitted {
		return "", false, nil
	}
	return string(res.value), true, nil
}

// ---- 2. Interactive TUI login approval view ---------------------------------

type loginTickMsg struct{}
type loginPollResult struct {
	status, token, email string
	err                  error
}

type loginModel struct {
	base, link, code, poll string
	deadline               time.Time
	spin                   int
	width, height          int
	color                  bool
	notice                 string
	done                   bool
	approved               config
	err                    error
}

func loginTick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return loginTickMsg{} })
}

func pollLoginCmd(base, poll string) tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg {
		var res struct {
			Status string `json:"status"`
			Token  string `json:"token"`
			Email  string `json:"email"`
		}
		err := call(config{Server: base}, http.MethodPost, "/api/cli/login/poll", map[string]string{"poll": poll}, &res)
		return loginPollResult{status: res.Status, token: res.Token, email: res.Email, err: err}
	})
}

func (m loginModel) Init() tea.Cmd {
	return tea.Batch(loginTick(), pollLoginCmd(m.base, m.poll))
}

func (m loginModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = v.Width, v.Height
	case loginTickMsg:
		if m.done {
			return m, nil
		}
		if time.Now().After(m.deadline) {
			m.done = true
			m.err = errors.New("the code ran out before it was approved; try again")
			return m, tea.Quit
		}
		m.spin = (m.spin + 1) % len(spinnerFrames)
		return m, loginTick()
	case loginPollResult:
		if m.done {
			return m, nil
		}
		if v.err != nil {
			return m, pollLoginCmd(m.base, m.poll)
		}
		switch v.status {
		case "approved":
			cfg := config{Server: m.base, Token: v.token, Email: v.email}
			if err := saveConfig(cfg); err != nil {
				m.err = err
			} else {
				m.approved = cfg
			}
			m.done = true
			return m, tea.Quit
		case "denied":
			m.done = true
			m.err = errors.New("the dashboard denied this computer")
			return m, tea.Quit
		case "expired":
			m.done = true
			m.err = errors.New("the code ran out before it was approved; try again")
			return m, tea.Quit
		default:
			return m, pollLoginCmd(m.base, m.poll)
		}
	case tea.KeyMsg:
		switch v.String() {
		case "esc", "q", "ctrl+c":
			m.done = true
			m.err = errCancelled
			return m, tea.Quit
		case "o":
			openBrowser(m.link)
			m.notice = "Reopened approval link in your browser"
		}
	}
	return m, nil
}

func (m loginModel) View() string {
	inner := max(28, min(72, m.width-8))
	c := m.color
	var out []string
	if m.height >= 22 && inner >= 46 {
		out = append(out, "", brand(inner+4, c), "")
	} else {
		out = append(out, "", "  "+miniMark(c)+"  "+boldInk("PIER", fgBright, c))
	}
	out = append(out, cardTop(inner, c))
	left := boldInk("Approve this computer", fgBright, c)
	right := ink("● ", lavender, c) + ink(host(m.base), fgMuted, c)
	if gap := inner - ansi.StringWidth(left) - ansi.StringWidth(right); gap >= 2 {
		out = append(out, cardRow(left+strings.Repeat(" ", gap)+right, inner, c))
	} else {
		out = append(out, cardRow(left, inner, c))
	}
	out = append(out, cardDivider(inner, c))
	out = append(out, cardRow("", inner, c))
	out = append(out, cardRow("  "+ink("Verification code", fgDim, c), inner, c))
	out = append(out, cardRow(bgLine("  "+boldInk(m.code, pink, c), bgActive, inner, c), inner, c))
	out = append(out, cardRow("", inner, c))
	out = append(out, cardRow("  "+ink("Dashboard link", fgDim, c), inner, c))
	out = append(out, cardRow("  "+ink(m.link, fgSecondary, c), inner, c))
	out = append(out, cardRow("", inner, c))
	status := ink(spinnerFrames[m.spin]+" ", lavender, c) + ink("Waiting for approval in your browser…", fgMuted, c)
	out = append(out, cardRow("  "+status, inner, c))
	if m.notice != "" {
		out = append(out, cardDivider(inner, c))
		out = append(out, cardRow("  "+ink("✓ "+m.notice, mint, c), inner, c))
	}
	out = append(out, cardBottom(inner, c))
	sep := "    "
	hints := strings.Join([]string{keyHint("o", "open browser", c), keyHint("esc", "cancel", c)}, sep)
	out = append(out, "  "+ansi.Truncate(hints, inner+4, "…"))
	return strings.Join(out, "\n")
}

func openLoginView(base string) (config, error) {
	name, _ := os.Hostname()
	name = strings.TrimSuffix(name, ".local")
	var start struct {
		Code        string `json:"code"`
		Poll        string `json:"poll"`
		ApprovePath string `json:"approve_path"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := call(config{Server: base}, http.MethodPost, "/api/cli/login", map[string]string{"name": name}, &start); err != nil {
		return config{}, err
	}
	link := base + start.ApprovePath
	openBrowser(link)
	m := loginModel{
		base:     base,
		link:     link,
		code:     start.Code,
		poll:     start.Poll,
		deadline: time.Now().Add(time.Duration(start.ExpiresIn) * time.Second),
		width:    80,
		height:   26,
		color:    os.Getenv("NO_COLOR") == "",
	}
	final, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	if err != nil {
		return config{}, err
	}
	res := final.(loginModel)
	if res.err != nil {
		return config{}, res.err
	}
	return res.approved, nil
}

// ---- 3. Interactive TUI live deploy view ------------------------------------

type deployEvent struct {
	phase string
	row   *logRow
	url   string
	done  bool
	err   error
}

type deployTickMsg struct{}

type deployView struct {
	project, target, dir string
	started              time.Time
	in                   <-chan deployEvent
	rows, frozen         []logRow
	phase, liveURL       string
	width, height        int
	offset, unseen, spin int
	paused, done, color  bool
	retry                bool
	notice               string
	err                  error
}

func deployTick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return deployTickMsg{} })
}

func (m deployView) readNext() tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-m.in
		if !ok {
			return deployEvent{done: true}
		}
		return ev
	}
}

func (m deployView) Init() tea.Cmd {
	return tea.Batch(deployTick(), m.readNext())
}

func (m *deployView) pause() {
	if !m.paused {
		m.frozen = slices.Clone(m.rows)
		m.paused = true
	}
}

func (m *deployView) resume() {
	m.paused = false
	m.frozen = nil
	m.offset = 0
	m.unseen = 0
}

func (m deployView) bodyHeight() int   { return max(4, m.height-9) }
func (m deployView) contentWidth() int { return max(20, m.width-4) }

func (m deployView) body(offset int) ([]string, int) {
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

func (m deployView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = v.Width, v.Height
	case deployTickMsg:
		if m.done {
			return m, nil
		}
		m.spin = (m.spin + 1) % len(spinnerFrames)
		return m, deployTick()
	case deployEvent:
		if v.phase != "" {
			m.phase = v.phase
		}
		if v.url != "" && m.liveURL == "" {
			m.liveURL = v.url
		}
		if v.row != nil {
			m.rows = append(m.rows, *v.row)
			if len(m.rows) > logHistory {
				m.rows = m.rows[len(m.rows)-logHistory:]
			}
			if m.paused {
				m.unseen++
			}
		}
		if v.done {
			m.done = true
			m.err = v.err
			return m, nil
		}
		return m, m.readNext()
	case tea.KeyMsg:
		m.notice = ""
		switch v.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "enter":
			if m.done {
				return m, tea.Quit
			}
		case "o":
			if m.liveURL != "" {
				openBrowser(m.liveURL)
				m.notice = "Opened " + m.liveURL
			}
		case "r":
			if m.done {
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
			_, avail := m.body(m.offset + step)
			m.offset = avail
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

func (m deployView) View() string {
	w := m.contentWidth()
	c := m.color
	clip := func(s string) string { return "  " + ansi.Truncate(s, w, "…") }

	var out []string
	ctx := boldInk(cleanLog(m.project), fgBright, c) + "  " + ink(m.target, pink, c)
	out = append(out, clip(miniMark(c)+"  "+boldInk("PIER", fgBright, c)+"  "+ink("/", fgDim, c)+"  "+ctx))

	title := boldInk("Deploy "+m.target, fgBright, c)
	elapsed := fmt.Sprintf("%.1fs", time.Since(m.started).Seconds())
	var badge string
	switch {
	case m.done && m.err != nil:
		badge = ink("● ", coralTxt, c) + boldInk("Failed", coralTxt, c)
	case m.done:
		badge = ink("● ", mint, c) + boldInk("Ready", mint, c)
	case m.paused:
		lbl := "Paused"
		if m.unseen > 0 {
			lbl += fmt.Sprintf(", %d new", m.unseen)
		}
		badge = ink("● ", amber, c) + boldInk(lbl, amber, c)
	default:
		lbl := m.phase
		if lbl == "" {
			lbl = "Building"
		}
		badge = ink(spinnerFrames[m.spin]+" ", lavender, c) + boldInk(lbl+"  "+elapsed, lavender, c)
	}
	gap := max(2, w-ansi.StringWidth(title)-ansi.StringWidth(badge))
	out = append(out, clip(title+strings.Repeat(" ", gap)+badge))
	out = append(out, clip(ink(strings.Repeat("─", w), fgBorder, c)))
	out = append(out, clip(ink(fmt.Sprintf("%8s  %-6s  %s", "ELAPSED", "STAGE", "OUTPUT"), fgDim, c)))

	lines, _ := m.body(m.offset)
	if len(lines) == 0 {
		lines = []string{ink("Preparing upload from "+filepath.Base(m.dir)+"…", fgMuted, c)}
	}
	for _, line := range lines {
		out = append(out, clip(line))
	}
	for len(lines) < m.bodyHeight() {
		out = append(out, "")
		lines = append(lines, "")
	}

	out = append(out, clip(ink(strings.Repeat("─", w), fgBorder, c)))

	// Summary / URL bar.
	var statusLine string
	switch {
	case m.notice != "":
		statusLine = ink("✓ "+m.notice, mint, c)
	case m.done && m.err != nil:
		statusLine = ink("✗ "+cleanLog(m.err.Error()), coralTxt, c)
	case m.done && m.liveURL != "":
		statusLine = ink("✓ Live at ", mint, c) + boldInk(m.liveURL, fgBright, c)
	case m.done:
		statusLine = ink("✓ Deployment complete", mint, c)
	case m.paused:
		statusLine = ink("Scroll paused. The build carries on.", amber, c)
	default:
		statusLine = ink(filepath.Base(m.dir)+" → "+m.project+" ("+m.target+")", fgDim, c)
	}
	out = append(out, clip(statusLine))

	sep := "    "
	if w < 46 {
		sep = "  "
	}
	var hints []string
	if m.done {
		if m.liveURL != "" {
			hints = append(hints, keyHint("o", "open", c))
		}
		hints = append(hints, keyHint("r", "redeploy", c), keyHint("↑↓", "scroll", c), keyHint("esc", "done", c))
	} else {
		if m.paused {
			hints = append(hints, keyHint("space", "resume", c))
		} else {
			hints = append(hints, keyHint("space", "pause", c))
		}
		hints = append(hints, keyHint("↑↓", "scroll", c), keyHint("esc", "close", c))
	}
	out = append(out, clip(strings.Join(hints, sep)))
	return strings.Join(out, "\n")
}

func parseNarrationRow(line string, started time.Time) (logRow, string) {
	elapsed := fmt.Sprintf("+%.1fs", time.Since(started).Seconds())
	tag := "pier"
	msg := line
	if len(line) >= 8 {
		candidate := strings.TrimSpace(line[:8])
		if candidate == "pier" || candidate == "build" || candidate == "deploy" || candidate == "alias" {
			tag = candidate
			msg = strings.TrimSpace(line[8:])
		}
	}
	tone := lavender
	switch tag {
	case "build":
		tone = mint
	case "deploy", "alias":
		tone = pink
	}
	foundURL := ""
	if tag == "alias" && (strings.HasPrefix(msg, "https://") || strings.HasPrefix(msg, "http://")) {
		foundURL = strings.Fields(msg)[0]
	}
	return logRow{stamp: elapsed, clock: elapsed, label: tag, message: cleanLog(msg), tone: tone}, foundURL
}

func openDeployView(c config, project, dir, target string, g git, initialNotes []string) (string, error) {
	for {
		ctx, cancel := context.WithCancel(context.Background())
		events := make(chan deployEvent, 32)
		started := time.Now()

		go func() {
			defer close(events)
			emit := func(ev deployEvent) bool {
				select {
				case events <- ev:
					return true
				case <-ctx.Done():
					return false
				}
			}
			for _, note := range initialNotes {
				row, _ := parseNarrationRow("pier    "+note, started)
				emit(deployEvent{row: &row})
			}
			emit(deployEvent{phase: "Packing"})
			files, err := bundle.Files(dir)
			if err != nil {
				emit(deployEvent{done: true, err: err})
				return
			}
			if len(files) == 0 {
				emit(deployEvent{done: true, err: fmt.Errorf("%s has nothing to upload", dir)})
				return
			}
			tmp, err := os.CreateTemp("", "pier-upload-*.tar.gz")
			if err != nil {
				emit(deployEvent{done: true, err: err})
				return
			}
			defer os.Remove(tmp.Name())
			defer tmp.Close()

			size, err := bundle.Pack(tmp, dir, files)
			if err != nil {
				emit(deployEvent{done: true, err: fmt.Errorf("packing %s: %w", dir, err)})
				return
			}
			packed, _ := tmp.Seek(0, 1)
			if _, err := tmp.Seek(0, 0); err != nil {
				emit(deployEvent{done: true, err: err})
				return
			}

			uploadMsg := fmt.Sprintf("Uploading %s (%s, %s packed) to %s, for %s",
				plural(len(files), "file"), human(size), human(packed), project, target)
			row, _ := parseNarrationRow("pier    "+uploadMsg, started)
			emit(deployEvent{phase: "Uploading", row: &row})

			q := url.Values{
				"target": {target}, "files": {strconv.Itoa(len(files))},
				"branch": {g.branch}, "commit": {g.commit}, "message": {g.message},
				"tools": {localTools(dir)},
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Server+"/api/projects/"+url.PathEscape(project)+"/deployments?"+q.Encode(), tmp)
			if err != nil {
				emit(deployEvent{done: true, err: err})
				return
			}
			req.ContentLength = packed
			req.Header.Set("Content-Type", "application/gzip")
			req.Header.Set("Authorization", "Bearer "+c.Token)
			resp, err := client.Do(req)
			if err != nil {
				emit(deployEvent{done: true, err: fmt.Errorf("can't reach Pier at %s: %w", c.Server, err)})
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				var e struct{ Error string }
				json.NewDecoder(resp.Body).Decode(&e)
				if e.Error == "" {
					e.Error = "Pier answered " + resp.Status
				}
				emit(deployEvent{done: true, err: errors.New(e.Error)})
				return
			}
			emit(deployEvent{phase: "Building"})
			sc := bufio.NewScanner(resp.Body)
			sc.Buffer(make([]byte, 64*1024), 1024*1024)
			for sc.Scan() {
				r, foundURL := parseNarrationRow(sc.Text(), started)
				if !emit(deployEvent{row: &r, url: foundURL}) {
					return
				}
			}
			if err := sc.Err(); err != nil {
				emit(deployEvent{done: true, err: fmt.Errorf("lost the connection while building: %w", err)})
				return
			}
			if resp.Trailer.Get("Pier-Result") != "ok" {
				emit(deployEvent{done: true, err: errors.New("the build failed; nothing changed")})
				return
			}
			emit(deployEvent{done: true})
		}()

		m := deployView{
			project: project,
			target:  target,
			dir:     dir,
			started: started,
			in:      events,
			phase:   "Packing",
			width:   80,
			height:  26,
			color:   os.Getenv("NO_COLOR") == "",
		}
		final, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
		cancel()
		if err != nil {
			return "", err
		}
		res := final.(deployView)
		if res.retry {
			initialNotes = nil
			continue
		}
		return res.liveURL, res.err
	}
}

// ---- 4. Interactive TUI command reference card ------------------------------

type helpModel struct {
	server, project string
	offset          int
	width, height   int
	color           bool
}

func (m helpModel) Init() tea.Cmd { return nil }

func (m helpModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch k := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = k.Width, k.Height
	case tea.KeyMsg:
		switch k.String() {
		case "esc", "q", "enter", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			m.offset = max(0, m.offset-1)
		case "down", "j":
			m.offset++
		}
	}
	return m, nil
}

func (m helpModel) View() string {
	inner := max(30, min(72, m.width-8))
	c := m.color
	var out []string
	if m.height >= 24 && inner >= 46 {
		out = append(out, "", brand(inner+4, c), "")
	} else {
		out = append(out, "", "  "+miniMark(c)+"  "+boldInk("PIER", fgBright, c))
	}
	out = append(out, cardTop(inner, c))
	left := boldInk("Command reference", fgBright, c)
	right := ""
	if m.server != "" {
		right = ink("● ", mint, c) + ink(m.server, fgMuted, c)
	}
	if right != "" && inner-ansi.StringWidth(left)-ansi.StringWidth(right) >= 2 {
		gap := inner - ansi.StringWidth(left) - ansi.StringWidth(right)
		out = append(out, cardRow(left+strings.Repeat(" ", gap)+right, inner, c))
	} else {
		out = append(out, cardRow(left, inner, c))
	}
	out = append(out, cardDivider(inner, c))

	var body []string
	for gi, g := range commandReferenceGroups() {
		if gi > 0 {
			body = append(body, "")
		}
		body = append(body, "  "+boldInk(strings.ToUpper(g.title), fgDim, c))
		for _, r := range g.rows {
			cmdW := min(30, max(18, inner/2-2))
			cmd := fmt.Sprintf("%-*s", cmdW, r[0])
			body = append(body, "  "+boldInk(cmd, fgBright, c)+"  "+ink(r[1], fgMuted, c))
		}
	}

	used := 0
	for _, s := range out {
		used += strings.Count(s, "\n") + 1
	}
	avail := max(4, m.height-used-4)
	off := min(m.offset, max(0, len(body)-avail))
	end := min(len(body), off+avail)
	for _, line := range body[off:end] {
		out = append(out, cardRow(line, inner, c))
	}
	out = append(out, cardBottom(inner, c))
	sep := "    "
	hints := strings.Join([]string{keyHint("↑↓", "scroll", c), keyHint("esc", "back", c)}, sep)
	out = append(out, "  "+ansi.Truncate(hints, inner+4, "…"))
	return strings.Join(out, "\n")
}

func openHelpView(server, project string) error {
	m := helpModel{
		server:  singleLine(server),
		project: singleLine(project),
		width:   80,
		height:  28,
		color:   os.Getenv("NO_COLOR") == "",
	}
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}
