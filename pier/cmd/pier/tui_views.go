package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
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
	c := m.color
	w := m.width
	inner := w - 4
	lines := []string{""}
	if m.cfg.label != "" {
		lines = append(lines, sgr(m.cfg.label, fgMuted, "", false, c))
	}
	// The field is the selected row's colour with a pink caret, so it
	// reads as the place the typing goes.
	var field string
	if len(m.value) == 0 && m.cfg.placeholder != "" {
		field = sgr("▏", pink, "", false, c) + sgr(m.cfg.placeholder, fgDim, "", false, c)
	} else {
		field = sgr(string(m.value[:m.cursor]), fgBright, "", true, c) + sgr("▏", pink, "", false, c) + sgr(string(m.value[m.cursor:]), fgBright, "", true, c)
	}
	lines = append(lines, lit(" "+field, inner, c), "")
	if m.errMsg != "" {
		lines = append(lines, sgr(singleLine(m.errMsg), coralTxt, "", false, c))
	} else if m.cfg.hint != "" {
		lines = append(lines, sgr(singleLine(m.cfg.hint), fgDim, "", false, c))
	}
	body := box(m.cfg.title, lines, w, bodyRows(m.height), true, c)
	bar := keyBar([]keyHelp{{"enter", "confirm"}, {"ctrl+u", "clear"}, {"esc", "back"}}, w, c)
	return screen(header(w, m.cfg.project, m.cfg.server, "", "", c), body, bar, w, m.height)
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
	c := m.color
	w := m.width
	lines := []string{
		"",
		sgr("Check the dashboard shows this code, then approve this computer there.", fgSecondary, "", false, c),
		"",
		sgr("Code", fgMuted, "", false, c),
		tiles(m.code, pink, len([]rune(m.code)), c),
		"",
		sgr("Dashboard link", fgMuted, "", false, c),
		sgr(m.link, fgBright, "", false, c),
		"",
		sgr(spinnerFrames[m.spin]+" ", lavender, "", false, c) + sgr("Waiting for approval in your browser…", fgSecondary, "", false, c),
	}
	if m.notice != "" {
		lines = append(lines, "", sgr("✓ "+m.notice, mint, "", false, c))
	}
	body := box("Approve this computer", lines, w, bodyRows(m.height), true, c)
	bar := keyBar([]keyHelp{{"o", "open the link again"}, {"esc", "cancel"}}, w, c)
	return screen(header(w, "", host(m.base), "", "", c), body, bar, w, m.height)
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

// ---- 3. Narration ------------------------------------------------------------

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
	foundURL := ""
	if tag == "alias" && (strings.HasPrefix(msg, "https://") || strings.HasPrefix(msg, "http://")) {
		foundURL = strings.Fields(msg)[0]
	}
	return logRow{stamp: elapsed, clock: elapsed, label: tag, message: cleanLog(msg), tone: narrationTone(tag)}, foundURL
}

// narrationTone colours a build log's tag column: pierd's own lines in
// blue, the build in violet as it builds, the deploy in teal as it's
// kept, and the address in pink as it goes live.
func narrationTone(tag string) string {
	switch tag {
	case "build":
		return lavender
	case "deploy":
		return mint
	case "alias":
		return pink
	}
	return blue
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
			m.offset = min(m.offset+1, max(0, len(m.lines())-(bodyRows(m.height)-2)))
		}
	}
	return m, nil
}

func (m helpModel) View() string {
	c := m.color
	w := m.width
	lines := m.lines()
	rows := bodyRows(m.height)
	off := min(m.offset, max(0, len(lines)-(rows-2)))
	body := box("Help", lines[off:], w, rows, true, c)
	bar := keyBar([]keyHelp{{"↑↓", "scroll"}, {"esc", "back"}}, w, c)
	return screen(header(w, m.project, m.server, "", "", c), body, bar, w, m.height)
}

// lines is the help's text: the cockpit's keys, then every command.
func (m helpModel) lines() []string {
	c := m.color
	inner := m.width - 4
	keyW := 14
	var lines []string
	row := func(k, does string) {
		lines = append(lines, sgr(fit(k, keyW), fgBright, "", true, c)+sgr(ansi.Truncate(does, max(0, inner-keyW), "…"), fgSecondary, "", false, c))
	}
	lines = append(lines, sgr("Keys", fgMuted, "", false, c))
	for _, k := range cockpitKeys {
		row(k[0], k[1])
	}
	cmdW := min(32, max(18, inner/2))
	for _, g := range commandReferenceGroups() {
		lines = append(lines, "", sgr(g.title, fgMuted, "", false, c))
		for _, r := range g.rows {
			lines = append(lines, sgr(fit(r[0], cmdW), fgBright, "", true, c)+sgr(ansi.Truncate(r[1], max(0, inner-cmdW), "…"), fgSecondary, "", false, c))
		}
	}
	return lines
}

// cockpitKeys is what each key does in the cockpit, for its help.
var cockpitKeys = [][2]string{
	{"tab  ←→", "move between the projects and the project"},
	{"↑↓  j k", "move in the pane you're in"},
	{"enter", "show a project, or open a deployment"},
	{"d", "deploy this folder as a preview"},
	{"D", "deploy this folder to production"},
	{"p", "promote the selected deployment"},
	{"r", "roll production back"},
	{"l", "logs: the project's requests, or a deployment's build"},
	{"b", "every address on the server"},
	{"o", "open the address"},
	{"/", "find a project, or a deployment"},
	{"?", "this help"},
	{"q", "quit"},
}
