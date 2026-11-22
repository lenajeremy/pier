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
	terminal "golang.org/x/term"
)

var errCancelled = errors.New("cancelled")

func interactive() bool {
	return term() && terminal.IsTerminal(int(os.Stdin.Fd()))
}

type choice struct {
	id      string
	title   string
	hint    string
	badge   string // a status, shown on the right as a dot and the word
	section string // a label shown before the first item of its group, when nothing is filtered
}

type menu struct {
	last                           click
	title, server, project, filter string
	notice, noticeTone             string
	items, visible                 []choice
	cursor, offset, width, height  int
	search, color, showKeys        bool
	chosen                         string
}

func (m *menu) match() {
	m.visible = nil
	q := strings.ToLower(strings.TrimSpace(m.filter))
	for _, item := range m.items {
		if q == "" || strings.Contains(strings.ToLower(item.title+" "+item.hint+" "+item.badge+" "+item.section), q) {
			m.visible = append(m.visible, item)
		}
	}
	if m.cursor >= len(m.visible) {
		m.cursor = max(0, len(m.visible)-1)
	}
}

func (m menu) Init() tea.Cmd { return nil }

func (m menu) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch k := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = k.Width, k.Height
	case tea.MouseMsg:
		return m.mouse(k)
	case tea.KeyMsg:
		m.notice = ""
		switch k.Type {
		case tea.KeyCtrlC:
			return m, tea.Quit
		case tea.KeyEsc:
			if !m.search {
				return m, tea.Quit
			}
			m.filter = ""
			m.search = false
			m.cursor = 0
			m.match()
		case tea.KeyEnter:
			if len(m.visible) > 0 {
				m.chosen = m.visible[m.cursor].id
				return m, tea.Quit
			}
		case tea.KeyUp:
			if len(m.visible) > 0 {
				if m.cursor == 0 {
					m.cursor = len(m.visible) - 1
				} else {
					m.cursor--
				}
			}
		case tea.KeyDown:
			if len(m.visible) > 0 {
				m.cursor = (m.cursor + 1) % len(m.visible)
			}
		case tea.KeyBackspace:
			if m.search && m.filter != "" {
				r := []rune(m.filter)
				m.filter = string(r[:len(r)-1])
				m.cursor = 0
				m.match()
			}
		case tea.KeyRunes, tea.KeySpace:
			text := k.String()
			if k.Type == tea.KeyRunes {
				text = string(k.Runes)
			}
			if !m.search && strings.HasPrefix(text, "/") {
				m.search = true
				text = strings.TrimPrefix(text, "/")
				m.cursor = 0
			}
			if m.search {
				m.filter += singleLine(text)
				m.cursor = 0
				m.match()
			} else if !k.Paste {
				switch text {
				case "j":
					if len(m.visible) > 0 {
						m.cursor = (m.cursor + 1) % len(m.visible)
					}
				case "k":
					if len(m.visible) > 0 {
						if m.cursor == 0 {
							m.cursor = len(m.visible) - 1
						} else {
							m.cursor--
						}
					}
				case "q":
					return m, tea.Quit
				default:
					if m.showKeys && len(text) == 1 && text[0] >= '1' && text[0] <= '9' {
						idx := int(text[0] - '1')
						if idx < len(m.visible) {
							m.cursor = idx
							m.chosen = m.visible[idx].id
							return m, tea.Quit
						}
					}
				}
			}
		}
	}
	return m, nil
}

func (m menu) View() string {
	v, _ := m.render()
	return v
}

// render draws the picker and notes where each item and key landed, so a
// click picks what's under it.
func (m menu) render() (string, []hit) {
	c := m.color
	w := m.width
	inner := w - 4
	rows := bodyRows(m.height)

	// Sections label groups of items while nothing is filtered.
	type line struct {
		text string
		item int
	}
	var all []line
	last := ""
	sections := !m.search && m.filter == ""
	selected := 0
	for i, item := range m.visible {
		if sections && item.section != "" && item.section != last {
			if len(all) > 0 {
				all = append(all, line{item: -1})
			}
			all = append(all, line{text: sgr(item.section, fgMuted, "", false, c), item: -1})
			last = item.section
		}
		if i == m.cursor {
			selected = len(all)
		}
		all = append(all, line{text: m.renderItem(i, inner), item: i})
	}
	var lines []string
	if len(m.visible) == 0 {
		lines = append(lines, sgr("Nothing matches", fgDim, "", false, c))
	}
	off := window(selected, m.offset, len(all), rows-2)
	var hits []hit
	for _, l := range all[off:min(len(all), off+max(0, rows-2))] {
		if l.item >= 0 {
			hits = append(hits, hit{y: len(lines), x1: w, kind: hitChoice, index: l.item})
		}
		lines = append(lines, l.text)
	}
	hits = place(hits, 0, bodyTop(m.height))
	title := m.title
	if m.filter != "" {
		title += ", matching " + m.filter
	}
	body := box(title, lines, w, rows, true, c)

	var bar string
	var barHits []hit
	switch {
	case m.notice != "":
		tone := m.noticeTone
		if tone == "" {
			tone = mint
		}
		bar = noticeBar(m.notice, tone, w, c)
	case m.search:
		bar = " " + sgr("/", fgSecondary, "", true, c) + " " + sgr(m.filter, fgBright, "", false, c) + sgr("▏", pink, "", false, c) + "    " +
			strings.TrimPrefix(keyBar([]keyHelp{{"↑↓", "move"}, {"enter", "choose"}, {"esc", "clear"}}, w/2, c), " ")
	default:
		keys := []keyHelp{{"↑↓", "move"}, {"enter", "choose"}}
		if m.showKeys && len(m.visible) > 1 {
			keys = append(keys, keyHelp{fmt.Sprintf("1-%d", min(9, len(m.visible))), "jump"})
		}
		keys = append(keys, keyHelp{"/", "find"}, keyHelp{"esc", "back"})
		bar, barHits = keyBarHits(keys, w, c)
	}
	hits = append(hits, atRow(barHits, m.height-1)...)
	return screen(header(w, m.project, m.server, "", "", c), body, bar, w, m.height), hits
}

// mouse picks an item clicked twice, or once after it was selected, and
// moves through the items with the wheel.
func (m menu) mouse(e tea.MouseMsg) (tea.Model, tea.Cmd) {
	if e.Action != tea.MouseActionPress {
		return m, nil
	}
	switch e.Button {
	case tea.MouseButtonWheelUp:
		return m.Update(tea.KeyMsg{Type: tea.KeyUp})
	case tea.MouseButtonWheelDown:
		return m.Update(tea.KeyMsg{Type: tea.KeyDown})
	case tea.MouseButtonLeft:
	default:
		return m, nil
	}
	_, hits := m.render()
	h, ok := hitAt(hits, e.X, e.Y)
	switch {
	case !ok:
		return m, nil
	case h.kind == hitKey:
		if h.key == "↑↓" || strings.Contains(h.key, "-") {
			return m, nil
		}
		return m.Update(keyMsg(h.key))
	}
	run := pressed(&m.last, h, m.cursor == h.index)
	m.cursor = h.index
	if run {
		return m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	}
	return m, nil
}

func (m menu) renderItem(idx, inner int) string {
	item := m.visible[idx]
	selected := idx == m.cursor
	c := m.color

	// The selected item has the pink bar a selection has everywhere else.
	// Number keys jump to the first nine items, so each shows its number.
	prefix := "  "
	if selected {
		prefix = sgr("▌ ", pink, "", false, c)
	}
	if m.showKeys && !m.search {
		num := "  "
		if idx < 9 {
			num = fmt.Sprintf("%d ", idx+1)
		}
		prefix += sgr(num, fgDim, "", false, c)
	}
	right := ""
	if item.hint != "" {
		right = sgr(singleLine(item.hint), fgDim, "", false, c)
	}
	if item.badge != "" {
		if right != "" {
			right += "  "
		}
		right += dotted(item.badge, statusInk(item.badge), c)
	}
	titleW := inner - ansi.StringWidth(prefix) - ansi.StringWidth(right) - 2
	if titleW < 8 {
		right = ""
		titleW = inner - ansi.StringWidth(prefix)
	}
	ink := fgSecondary
	if selected {
		ink = fgBright
	}
	row := prefix + sgr(fit(item.title, titleW), ink, "", selected, c)
	if right != "" {
		row += "  " + right
	}
	if selected {
		return lit(row, inner, c)
	}
	return row
}

func newMenu(title, server, project string, items []choice) menu {
	m := menu{
		title:    title,
		server:   singleLine(server),
		project:  singleLine(project),
		items:    items,
		width:    80,
		height:   26,
		color:    os.Getenv("NO_COLOR") == "",
		showKeys: true,
	}
	m.match()
	return m
}

func (m *menu) choose() (string, error) {
	m.chosen = ""
	final, err := tea.NewProgram(*m, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	if err != nil {
		return "", err
	}
	*m = final.(menu)
	return m.chosen, nil
}

func selectItem(title, server, project string, items []choice) (string, error) {
	m := newMenu(title, server, project, items)
	return m.choose()
}

func projectChoices(c config, create bool) (string, error) {
	var list struct {
		Projects []struct {
			Name   string
			Status string
		}
	}
	if err := callWith("Loading your projects", c, http.MethodGet, "/api/projects", nil, &list); err != nil {
		return "", err
	}
	var items []choice
	for _, p := range list.Projects {
		badge := p.Status
		if badge == "none" {
			badge = ""
		}
		items = append(items, choice{id: p.Name, title: p.Name, badge: badge})
	}
	if create {
		items = append(items, choice{id: "+", title: "Create a new project", hint: "new"})
	}
	if len(items) == 0 {
		return "", errors.New("no projects yet; run pier deploy to create one")
	}
	return selectItem("Choose a project", host(c.Server), "", items)
}

// home is what pier opens on a terminal: the sign-in first if this
// computer isn't signed in, then the cockpit.
func home() error {
	c := loadConfig()
	if c.Server == "" || c.Token == "" {
		entered, ok, err := promptInput(inputConfig{
			title:       "Sign in to Pier",
			label:       "Pier's address",
			placeholder: "pier.example.com",
			hint:        "The address of your Pier dashboard",
			validate: func(v string) error {
				_, err := serverURL(v)
				return err
			},
		})
		if err != nil || !ok {
			return err
		}
		base, err := serverURL(entered)
		if err != nil {
			return err
		}
		if c, err = openLoginView(base); err != nil {
			return err
		}
	}
	return openCockpit(c, homeView, "", projectPane, nil)
}

type deploymentSummary struct {
	ID, Branch, Commit, Message, Status, Role, Framework, Runtime, URL, Error, Target, Source string
	BuildMS                                                                                   int64     `json:"build_ms"`
	CreatedAt                                                                                 time.Time `json:"created_at"`
	SentBy                                                                                    string    `json:"sent_by"` // who uploaded or rebuilt it
}
