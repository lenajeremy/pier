package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"pier/internal/apphost"
	"pier/internal/board"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	terminal "golang.org/x/term"
)

var errCancelled = errors.New("cancelled")

const refreshChoiceID = "__refresh__"

func interactive() bool {
	return term() && terminal.IsTerminal(int(os.Stdin.Fd()))
}

type choice struct {
	id      string
	title   string
	hint    string
	icon    string // single glyph shown left of title
	badge   string // optional status pill rendered right-aligned (live, preview, ready, failed, etc.)
	section string // optional uppercase group label shown before this item (when not filtering)
}

type metaRow struct {
	label string
	value string
	tone  string // optional fg RGB; empty uses fgSecondary (value may already contain ANSI)
	raw   bool   // true when value is already styled with ANSI
}

type menu struct {
	title, server, project, filter   string
	meta                             []metaRow
	notice, noticeTone               string
	items, visible                   []choice
	cursor, width, height            int
	search, color, showKeys, refresh bool
	chosen                           string
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
				case "r":
					if m.refresh {
						m.chosen = refreshChoiceID
						return m, tea.Quit
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
	// Outer card width fits cleanly in 76 columns max, down to narrow panes.
	inner := max(22, min(72, m.width-8))
	c := m.color
	var out []string

	// Top brand hero (auto-compacts on short terminals or dense detail views).
	minHeroHeight := 22
	if len(m.meta) > 0 {
		minHeroHeight = 25 + len(m.meta)
	}
	if m.height >= minHeroHeight && inner >= 46 {
		out = append(out, "", brand(inner+4, c), "")
	} else {
		out = append(out, "", "  "+miniMark(c)+"  "+boldInk("PIER", fgBright, c))
	}

	// Card frame top + title / live server-context or filter bar.
	out = append(out, cardTop(inner, c))
	var headLeft, headRight string
	if m.search {
		headLeft = boldInk("/", pink, c) + " " + boldInk(m.filter, fgBright, c) + ink("▏", pink, c)
		headRight = ink(fmt.Sprintf("%d/%d", len(m.visible), len(m.items)), fgDim, c)
	} else {
		headLeft = boldInk(m.title, fgBright, c)
		if m.server != "" {
			dot := ink("● ", mint, c)
			srv := ink(m.server, fgMuted, c)
			if strings.Contains(strings.ToLower(m.server), "signed out") {
				dot = ink("○ ", fgDim, c)
				srv = ink(m.server, fgDim, c)
			}
			headRight = dot + srv
			if m.project != "" {
				headRight += "  " + ink("◆ ", pink, c) + boldInk(m.project, fgSecondary, c)
			}
		} else if len(m.visible) > 0 {
			headRight = ink(fmt.Sprintf("%d/%d", m.cursor+1, len(m.visible)), fgDim, c)
		}
	}
	if headRight != "" && inner-ansi.StringWidth(headLeft)-ansi.StringWidth(headRight) >= 2 {
		gap := inner - ansi.StringWidth(headLeft) - ansi.StringWidth(headRight)
		out = append(out, cardRow(headLeft+strings.Repeat(" ", gap)+headRight, inner, c))
	} else if m.project != "" && !m.search {
		shortRight := ink("◆ ", pink, c) + boldInk(m.project, fgSecondary, c)
		if gap := inner - ansi.StringWidth(headLeft) - ansi.StringWidth(shortRight); gap >= 2 {
			out = append(out, cardRow(headLeft+strings.Repeat(" ", gap)+shortRight, inner, c))
		} else {
			out = append(out, cardRow(headLeft, inner, c))
		}
	} else {
		out = append(out, cardRow(headLeft, inner, c))
	}
	out = append(out, cardDivider(inner, c))

	// Optional metadata summary block (used by Deployment Detail pages).
	if len(m.meta) > 0 {
		labelW := 0
		for _, r := range m.meta {
			labelW = max(labelW, len(r.label))
		}
		for _, r := range m.meta {
			lbl := ink(fmt.Sprintf("  %-*s", labelW, r.label), fgDim, c)
			val := r.value
			if !r.raw {
				tone := r.tone
				if tone == "" {
					tone = fgSecondary
				}
				val = ink(singleLine(r.value), tone, c)
			}
			out = append(out, cardRow(lbl+"  "+val, inner, c))
		}
		out = append(out, cardDivider(inner, c))
	}

	// Compute viewport height available inside the card.
	usedLines := 0
	for _, s := range out {
		usedLines += strings.Count(s, "\n") + 1
	}
	reserveBottom := 4
	if m.notice != "" {
		reserveBottom += 2
	}
	availRows := max(3, m.height-usedLines-reserveBottom)

	// Build display rows (items + optional section headings & clean breathing gaps when not searching).
	type displayRow struct {
		isSection bool
		isSpacer  bool
		label     string
		itemIdx   int
	}
	var allRows []displayRow
	lastSec := ""
	showSections := !m.search && m.filter == ""
	for i, item := range m.visible {
		if showSections && item.section != "" && item.section != lastSec {
			if len(allRows) > 0 && m.height >= 27 && len(m.meta) == 0 {
				allRows = append(allRows, displayRow{isSpacer: true})
			}
			allRows = append(allRows, displayRow{isSection: true, label: strings.ToUpper(item.section)})
			lastSec = item.section
		}
		allRows = append(allRows, displayRow{itemIdx: i})
	}

	// Scroll window centered cleanly on the selected item's display row.
	selectedRow := 0
	for i, r := range allRows {
		if !r.isSection && !r.isSpacer && r.itemIdx == m.cursor {
			selectedRow = i
			break
		}
	}
	start := 0
	if len(allRows) > availRows {
		start = max(0, selectedRow-availRows+2)
		if start+availRows > len(allRows) {
			start = len(allRows) - availRows
		}
		if start > 0 && allRows[start-1].isSection && selectedRow == start {
			start--
		}
	}
	end := min(len(allRows), start+availRows)

	if len(m.visible) == 0 {
		out = append(out, cardRow(ink("  No matching items", fgMuted, c), inner, c))
	} else {
		for _, dr := range allRows[start:end] {
			if dr.isSpacer {
				out = append(out, cardRow("", inner, c))
				continue
			}
			if dr.isSection {
				out = append(out, cardRow(ink("  "+dr.label, fgDim, c), inner, c))
				continue
			}
			out = append(out, cardRow(m.renderItem(dr.itemIdx, inner), inner, c))
		}
	}

	if m.notice != "" {
		tone := m.noticeTone
		if tone == "" {
			tone = mint
		}
		out = append(out, cardDivider(inner, c))
		out = append(out, cardRow("  "+ink(singleLine(m.notice), tone, c), inner, c))
	}

	out = append(out, cardBottom(inner, c))

	// Styled footer keycap strip — automatically drops optional hints before truncating.
	sep := ink("  ·  ", fgBorder, c)
	if inner < 46 {
		sep = "  "
	}
	joinHints := func(hs []string) string { return strings.Join(hs, sep) }
	var hints []string
	if m.search {
		hints = []string{
			keyHint("↑↓", "move", c),
			keyHint("enter", "select", c),
			keyHint("esc", "clear", c),
		}
	} else {
		hints = []string{keyHint("↑↓", "move", c), keyHint("enter", "select", c)}
		optional := []string{}
		if m.refresh {
			optional = append(optional, keyHint("r", "refresh", c))
		}
		if len(m.items) > 4 || !m.refresh {
			optional = append(optional, keyHint("/", "filter", c))
		}
		if m.showKeys && len(m.visible) > 0 {
			optional = append(optional, keyHint(fmt.Sprintf("1-%d", min(9, len(m.visible))), "jump", c))
		}
		back := keyHint("esc", "back", c)
		for _, opt := range optional {
			candidate := append(append([]string{}, hints...), opt, back)
			if ansi.StringWidth(joinHints(candidate)) <= inner+4 {
				hints = append(hints, opt)
			}
		}
		hints = append(hints, back)
	}
	out = append(out, "  "+ansi.Truncate(joinHints(hints), inner+4, "…"))
	return strings.Join(out, "\n")
}

func (m menu) renderItem(idx, inner int) string {
	item := m.visible[idx]
	selected := idx == m.cursor
	c := m.color

	glyph := item.icon
	if glyph == "" {
		glyph = "•"
	}
	var prefix string
	if selected {
		prefix = ink("▌ ", pink, c) + ink(glyph, pink, c) + " "
	} else {
		prefix = "  " + ink(glyph, fgDim, c) + " "
	}
	if m.showKeys && !m.search && inner >= 52 {
		if idx < 9 {
			num := fmt.Sprintf("%d ", idx+1)
			if selected {
				prefix += ink(num, fgMuted, c)
			} else {
				prefix += ink(num, fgDim, c)
			}
		} else {
			prefix += "  "
		}
	}

	// Right-side badge and/or hint.
	right := ""
	if item.badge != "" && item.hint != "" && inner >= 56 {
		hintTone := fgDim
		if selected {
			hintTone = fgMuted
		}
		right = ink(item.hint, hintTone, c) + "  " + statusPill(item.badge, c)
	} else if item.badge != "" {
		right = statusPill(item.badge, c)
	} else if item.hint != "" && inner >= 44 {
		if selected {
			right = ink(item.hint, fgSecondary, c)
		} else {
			right = ink(item.hint, fgDim, c)
		}
	}

	rightWidth := ansi.StringWidth(right)
	prefixWidth := ansi.StringWidth(prefix)
	maxTitle := inner - prefixWidth - 1
	if rightWidth > 0 {
		maxTitle -= rightWidth + 2
	}
	if maxTitle < 6 && item.badge != "" {
		right = statusPill(item.badge, c)
		rightWidth = ansi.StringWidth(right)
		maxTitle = inner - prefixWidth - rightWidth - 3
	}
	if maxTitle < 6 {
		right = ""
		rightWidth = 0
		maxTitle = max(4, inner-prefixWidth-1)
	}

	title := ansi.Truncate(item.title, maxTitle, "…")
	if selected {
		title = boldInk(title, fgBright, c)
	} else {
		title = ink(title, fgSecondary, c)
	}

	line := prefix + title
	if right != "" {
		gap := max(2, inner-ansi.StringWidth(line)-rightWidth-1)
		line += strings.Repeat(" ", gap) + right + " "
	}
	if selected {
		return bgLine(line, bgActive, inner, c)
	}
	return line
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
	final, err := tea.NewProgram(*m, tea.WithAltScreen()).Run()
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
	if err := call(c, http.MethodGet, "/api/projects", nil, &list); err != nil {
		return "", err
	}
	var items []choice
	for _, p := range list.Projects {
		badge := p.Status
		if badge == "none" {
			badge = ""
		}
		items = append(items, choice{id: p.Name, title: p.Name, icon: "◆", badge: badge})
	}
	if create {
		items = append(items, choice{id: "+", title: "Create a new project", hint: "new", icon: "+"})
	}
	if len(items) == 0 {
		return "", errors.New("no projects yet; run pier deploy to create one")
	}
	return selectItem("Choose a project", host(c.Server), "", items)
}

func home() error {
	c := loadConfig()
	project := ""
	if c.Server != "" && c.Token != "" {
		if l, err := readLink("."); err == nil && (l.Server == "" || l.Server == c.Server) {
			project = l.Project
		}
	}

	buildHomeMenu := func(notice, noticeTone string, cursor int) menu {
		serverLabel := "Signed out"
		if c.Server != "" && c.Token != "" {
			serverLabel = host(c.Server)
		}
		var items []choice
		if c.Server != "" && c.Token != "" {
			items = []choice{
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
			}
		} else {
			items = []choice{
				{id: "login", title: "Sign in to a Pier server", hint: "pier login", icon: "▸", section: "Account"},
				{id: "help", title: "Command reference", hint: "pier help", icon: "?", section: "Account"},
			}
		}
		m := newMenu("Command palette", serverLabel, project, items)
		m.notice, m.noticeTone = notice, noticeTone
		m.cursor = min(cursor, max(0, len(items)-1))
		return m
	}

	notice, noticeTone := "", ""
	cursor := 0
	for {
		commands := buildHomeMenu(notice, noticeTone, cursor)
		action, err := commands.choose()
		if err != nil || action == "" {
			return err
		}
		cursor = commands.cursor
		notice, noticeTone = "", ""
		serverLabel := "Signed out"
		if c.Server != "" && c.Token != "" {
			serverLabel = host(c.Server)
		}

		switch action {
		case "help":
			if err := openHelpView(serverLabel, project); err != nil {
				notice, noticeTone = err.Error(), coralTxt
			}
		case "login":
			entered, ok, err := promptInput(inputConfig{
				title:       "Sign in to Pier",
				label:       "Pier server address",
				placeholder: "pier.example.com",
				hint:        "Enter your Pier dashboard domain or URL",
				validate: func(v string) error {
					_, err := serverURL(v)
					return err
				},
			})
			if err != nil {
				notice, noticeTone = err.Error(), coralTxt
				continue
			}
			if !ok {
				continue
			}
			base, err := serverURL(entered)
			if err != nil {
				notice, noticeTone = err.Error(), coralTxt
				continue
			}
			cfg, err := openLoginView(base)
			if err != nil {
				if !errors.Is(err, errCancelled) {
					notice, noticeTone = err.Error(), coralTxt
				}
				continue
			}
			c = cfg
			project = ""
			if l, err := readLink("."); err == nil && (l.Server == "" || l.Server == c.Server) {
				project = l.Project
			}
			cursor = 0
			notice, noticeTone = "✓ Signed in to "+host(c.Server)+" as "+c.Email, mint
		case "logout":
			confirm, err := selectItem("Sign out of "+serverLabel+"?", serverLabel, project, []choice{
				{id: "yes", title: "Sign out of this computer", icon: "⇥"},
				{id: "no", title: "Cancel", icon: "←"},
			})
			if err != nil || confirm != "yes" {
				continue
			}
			if c.Server != "" {
				_ = call(c, http.MethodDelete, "/api/session", nil, nil)
			}
			_ = os.Remove(configPath())
			c = config{}
			project = ""
			cursor = 0
			notice, noticeTone = "✓ Signed out of "+serverLabel, mint
		case "switch":
			chosen, err := projectChoices(c, false)
			if err != nil {
				notice, noticeTone = err.Error(), coralTxt
				continue
			}
			if chosen != "" {
				project = chosen
				notice, noticeTone = "✓ Active project: "+project, mint
			}
		case "board":
			if err := browseBoard(c); err != nil && !errors.Is(err, errCancelled) {
				notice, noticeTone = err.Error(), coralTxt
			}
		case "preview", "prod":
			cwd, err := filepath.Abs(".")
			if err != nil {
				notice, noticeTone = err.Error(), coralTxt
				continue
			}
			promptTitle := "Deploy a preview"
			confirmLabel := "Deploy current folder"
			target := "preview"
			if action == "prod" {
				promptTitle = "Deploy to production"
				confirmLabel = "Deploy current folder to production"
				target = "production"
			}
			chosen, err := selectItem(promptTitle, serverLabel, project, []choice{
				{id: "cwd", title: confirmLabel, hint: filepath.Base(cwd), icon: "▲"},
				{id: "other", title: "Choose another folder…", hint: "enter path", icon: "▸"},
				{id: "back", title: "Back", icon: "←"},
			})
			if err != nil || chosen == "" || chosen == "back" {
				continue
			}
			dir := cwd
			if chosen == "other" {
				entered, ok, err := promptInput(inputConfig{
					title:       promptTitle,
					server:      serverLabel,
					project:     project,
					label:       "Folder path",
					initial:     cwd,
					placeholder: cwd,
					hint:        "Path to the folder you want to pack and deploy",
					validate: func(v string) error {
						if v == "" {
							return errors.New("enter a folder path")
						}
						p, err := filepath.Abs(v)
						if err != nil {
							return err
						}
						fi, err := os.Stat(p)
						if err != nil || !fi.IsDir() {
							return fmt.Errorf("%s is not a folder", v)
						}
						return nil
					},
				})
				if err != nil {
					notice, noticeTone = err.Error(), coralTxt
					continue
				}
				if !ok {
					continue
				}
				dir, _ = filepath.Abs(entered)
			}
			g := gitInfo(dir)
			deployProj, notes, err := chooseProject(c, dir, project, true)
			if err != nil {
				if !errors.Is(err, errCancelled) {
					notice, noticeTone = err.Error(), coralTxt
				}
				continue
			}
			project = deployProj
			liveURL, err := openDeployView(c, project, dir, target, g, notes)
			if err != nil {
				if !errors.Is(err, errCancelled) {
					notice, noticeTone = err.Error(), coralTxt
				}
				continue
			}
			if liveURL != "" {
				notice, noticeTone = "✓ Deployed "+project+" → "+liveURL, mint
			} else {
				notice, noticeTone = "✓ Deployed "+project+" ("+target+")", mint
			}
		default:
			name := project
			if name == "" {
				name, err = projectChoices(c, false)
				if err != nil {
					notice, noticeTone = err.Error(), coralTxt
					continue
				}
				if name == "" {
					continue
				}
				project = name
			}
			if action == "history" {
				if err := browseDeployments(c, name); err != nil && !errors.Is(err, errCancelled) {
					notice, noticeTone = err.Error(), coralTxt
				}
				continue
			}
			args := []string{name, "--limit", "50"}
			if action == "runtime" {
				args = append(args, "--runtime")
			}
			if action == "build" {
				args = append(args, "--build")
			}
			if err := logs(args); err != nil && !errors.Is(err, errCancelled) {
				notice, noticeTone = err.Error(), coralTxt
			}
		}
	}
}

type deploymentSummary struct {
	ID, Branch, Commit, Message, Status, Role, Framework, Runtime, URL, Error string
	BuildMS                                                                   int64     `json:"build_ms"`
	CreatedAt                                                                 time.Time `json:"created_at"`
}

// browseDeployments opens the interactive deployment history TUI for a project.
func browseDeployments(c config, project string) error {
	cursor := 0
	for {
		var res struct {
			Deployments []deploymentSummary
		}
		if err := call(c, http.MethodGet, "/api/projects/"+url.PathEscape(project)+"/deployments", nil, &res); err != nil {
			return err
		}
		items := make([]choice, 0, len(res.Deployments))
		for _, d := range res.Deployments {
			msg := cleanLog(strings.TrimSpace(d.Message))
			title := d.ID
			if msg != "" {
				title += "  " + msg
			}
			var hintParts []string
			if d.Branch != "" {
				hintParts = append(hintParts, cleanLog(d.Branch))
			}
			if !d.CreatedAt.IsZero() {
				hintParts = append(hintParts, ago(d.CreatedAt))
			}
			items = append(items, choice{
				id:    d.ID,
				title: title,
				hint:  strings.Join(hintParts, " · "),
				icon:  "◷",
				badge: d.Role,
			})
		}
		if len(items) == 0 {
			_, err := selectItem("No deployments yet", host(c.Server), project, []choice{{id: "back", title: "Back", icon: "←"}})
			return err
		}
		history := newMenu("Deployment history", host(c.Server), project, items)
		history.refresh = true
		history.cursor = min(cursor, len(items)-1)
		id, err := history.choose()
		if err != nil || id == "" {
			return err
		}
		cursor = history.cursor
		if id == refreshChoiceID {
			continue
		}
		if err := inspectDeployment(c, project, id); err != nil {
			return err
		}
	}
}

// inspectDeployment renders an interactive deployment detail page with live
// status, container health, URLs, logs, promote, restart, rollback, and redeploy.
func inspectDeployment(c config, project, id string) error {
	notice, noticeTone := "", ""
	cursor := 0
	for {
		var detail struct {
			Deployment deploymentSummary
			Addresses  []struct {
				Host, URL, Kind string
			}
			Container *apphost.ContainerStatus
		}
		path := "/api/projects/" + url.PathEscape(project) + "/deployments/" + url.PathEscape(id)
		if err := call(c, http.MethodGet, path, nil, &detail); err != nil {
			return err
		}
		d := detail.Deployment
		hasRuntime := d.Runtime != ""
		liveURL := d.URL
		if len(detail.Addresses) > 0 && detail.Addresses[0].URL != "" {
			liveURL = detail.Addresses[0].URL
		}

		color := os.Getenv("NO_COLOR") == ""
		statusText := statusPill(d.Role, color)
		if d.Framework != "" {
			kind := d.Framework
			if hasRuntime {
				kind += " (server)"
			}
			statusText += ink("  ·  "+kind, fgMuted, color)
		}
		meta := []metaRow{
			{label: "Status", value: statusText, raw: true},
		}
		if detail.Container != nil {
			meta = append(meta, metaRow{label: "Container", value: formatContainerMeta(detail.Container, color), raw: true})
		}
		if d.Status == "failed" && d.Error != "" {
			meta = append(meta, metaRow{label: "Error", value: cleanLog(d.Error), tone: coralTxt})
		}
		if liveURL != "" {
			meta = append(meta, metaRow{label: "URL", value: liveURL, tone: fgBright})
		}
		var srcParts []string
		if d.Branch != "" {
			srcParts = append(srcParts, cleanLog(d.Branch))
		}
		if d.Commit != "" {
			sha := d.Commit
			if len(sha) > 7 {
				sha = sha[:7]
			}
			srcParts = append(srcParts, sha)
		}
		if d.Message != "" {
			srcParts = append(srcParts, cleanLog(d.Message))
		}
		if len(srcParts) > 0 {
			meta = append(meta, metaRow{label: "Source", value: strings.Join(srcParts, " · "), tone: fgSecondary})
		}
		var builtParts []string
		if d.BuildMS > 0 {
			builtParts = append(builtParts, fmt.Sprintf("%.1fs", float64(d.BuildMS)/1000))
		}
		if !d.CreatedAt.IsZero() {
			builtParts = append(builtParts, ago(d.CreatedAt))
		}
		if len(builtParts) > 0 {
			meta = append(meta, metaRow{label: "Built", value: strings.Join(builtParts, " · "), tone: fgMuted})
		}

		actions := []choice{
			{id: "build", title: "Build logs", hint: "build output", icon: "◇", section: "Logs"},
		}
		if hasRuntime && d.Status == "ready" {
			actions = append(actions, choice{id: "runtime", title: "Runtime logs", hint: "stdout / stderr", icon: "▸", section: "Logs"})
		}
		actions = append(actions, choice{id: "requests", title: "Request logs", hint: "project HTTP traffic", icon: "≡", section: "Logs"})
		if liveURL != "" && d.Status == "ready" {
			actions = append(actions, choice{id: "open", title: "Open in browser", hint: host(liveURL), icon: "↗", section: "Actions"})
		}
		if d.Status == "ready" && hasRuntime {
			actions = append(actions, choice{id: "restart", title: "Restart container", hint: "zero rebuild", icon: "↻", section: "Actions"})
		}
		if d.Status != "building" {
			actions = append(actions, choice{id: "redeploy", title: "Redeploy", hint: "rebuild with latest settings", icon: "▲", section: "Actions"})
		}
		if d.Status == "ready" && d.Role != "production" {
			actions = append(actions, choice{id: "promote", title: "Promote to production", hint: "pier promote " + id, icon: "✦", section: "Actions"})
		}
		if d.Role == "production" {
			actions = append(actions, choice{id: "rollback", title: "Roll back production", hint: "revert to previous", icon: "←", section: "Actions"})
		}

		m := newMenu("Deployment "+id, host(c.Server), project, actions)
		m.meta = meta
		m.refresh = true
		m.notice, m.noticeTone = notice, noticeTone
		m.cursor = min(cursor, len(actions)-1)

		kind, err := m.choose()
		if err != nil || kind == "" {
			return err
		}
		cursor = m.cursor
		notice, noticeTone = "", ""

		switch kind {
		case refreshChoiceID:
			continue
		case "open":
			openBrowser(liveURL)
			notice, noticeTone = "Opened "+liveURL, mint
		case "promote":
			var res productionState
			if err := call(c, http.MethodPost, "/api/projects/"+url.PathEscape(project)+"/promote", map[string]string{"id": id}, &res); err != nil {
				notice, noticeTone = err.Error(), coralTxt
				continue
			}
			notice, noticeTone = "✓ "+res.Address+" now serves "+res.serving(), mint
		case "restart":
			restartPath := "/api/projects/" + url.PathEscape(project) + "/deployments/" + url.PathEscape(id) + "/restart"
			if err := call(c, http.MethodPost, restartPath, map[string]string{}, nil); err != nil {
				notice, noticeTone = err.Error(), coralTxt
				continue
			}
			notice, noticeTone = "✓ Container restarted and passed readiness check", mint
		case "rollback":
			confirm, err := selectItem("Roll back production on "+project+"?", host(c.Server), project, []choice{
				{id: "yes", title: "Roll back to previous deployment", icon: "←"},
				{id: "no", title: "Cancel", icon: "×"},
			})
			if err != nil || confirm != "yes" {
				continue
			}
			var res productionState
			if err := call(c, http.MethodPost, "/api/projects/"+url.PathEscape(project)+"/rollback", map[string]string{}, &res); err != nil {
				notice, noticeTone = err.Error(), coralTxt
				continue
			}
			notice, noticeTone = "✓ Rolled back "+res.Address+" to "+res.serving(), mint
		case "redeploy":
			var res struct{ ID string }
			if err := call(c, http.MethodPost, "/api/projects/"+url.PathEscape(project)+"/redeploy", map[string]string{"id": id}, &res); err != nil {
				notice, noticeTone = err.Error(), coralTxt
				continue
			}
			if res.ID != "" {
				id = res.ID
				if err := logs([]string{project, "--build", "--deployment", id}); err != nil {
					notice, noticeTone = err.Error(), coralTxt
				}
			}
		default:
			args := []string{project}
			if kind != "requests" {
				args = append(args, "--"+kind, "--deployment", id)
			}
			if err := logs(args); err != nil {
				notice, noticeTone = err.Error(), coralTxt
			}
		}
	}
}

func formatContainerMeta(st *apphost.ContainerStatus, color bool) string {
	label := st.State
	if st.State == "oom_killed" {
		label = "oom killed"
	} else if st.State == "running" && st.Health == "unhealthy" {
		label = "unhealthy"
	}
	parts := []string{statusPill(label, color)}
	var details []string
	if st.MemoryLimitMB > 0 {
		if st.MemoryLimitMB >= 1024 && st.MemoryLimitMB%1024 == 0 {
			details = append(details, fmt.Sprintf("%d GB RAM", st.MemoryLimitMB/1024))
		} else {
			details = append(details, fmt.Sprintf("%d MB RAM", st.MemoryLimitMB))
		}
	}
	if st.CPULimit > 0 {
		details = append(details, fmt.Sprintf("%g vCPU", st.CPULimit))
	}
	if st.Restarts > 0 {
		details = append(details, fmt.Sprintf("%d restarts", st.Restarts))
	}
	if st.State == "running" && !st.StartedAt.IsZero() {
		details = append(details, "up "+ago(st.StartedAt))
	}
	if len(details) > 0 {
		parts = append(parts, ink("  ·  "+strings.Join(details, " · "), fgMuted, color))
	}
	return strings.Join(parts, "")
}

func browseBoard(c config) error {
	cursor := 0
	for {
		var res struct {
			Domain string
			Rows   []board.Row
		}
		if err := call(c, http.MethodGet, "/api/board", nil, &res); err != nil {
			return err
		}
		var items []choice
		for _, r := range res.Rows {
			hint := cleanLog(r.Deployment)
			if r.Branch != "" {
				hint = cleanLog(r.Branch) + " · " + hint
			}
			items = append(items, choice{
				id:    r.Address,
				title: cleanLog(r.Address),
				hint:  hint,
				icon:  "◈",
				badge: cleanLog(r.Status),
			})
		}
		if len(items) == 0 {
			_, err := selectItem("No addresses yet", host(c.Server), "", []choice{{id: "back", title: "Back", icon: "←"}})
			return err
		}
		boardMenu := newMenu("Deployment board", host(c.Server), "", items)
		boardMenu.refresh = true
		boardMenu.cursor = min(cursor, len(items)-1)
		address, err := boardMenu.choose()
		if err != nil || address == "" {
			return err
		}
		cursor = boardMenu.cursor
		if address == refreshChoiceID {
			continue
		}
		for _, r := range res.Rows {
			if r.Address != address {
				continue
			}
			if err := inspectDeployment(c, r.Project, r.Deployment); err != nil {
				return err
			}
			break
		}
	}
}
