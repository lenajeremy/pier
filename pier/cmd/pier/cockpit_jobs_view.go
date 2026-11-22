package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

var jobsKeys = []keyHelp{{"↑↓", "choose"}, {"enter", "open"}, {"tab", "actions"}, {"?", "help"}, {"q", "quit"}}

// actionBlock is the blank row, the Actions label and the action rows a
// screen ends a part with, and where the rows are.
func (m cockpit) actionBlock(lines []string, acts []action, inner, w int, focused bool) ([]string, []hit) {
	c := m.color
	var hits []hit
	lines = append(lines, "", sgr("Actions", fgMuted, "", false, c))
	cur := m.actionCursor(m.view, acts)
	for i, a := range acts {
		hits = append(hits, hit{y: len(lines), x1: w, kind: hitAction, index: i})
		lines = append(lines, actionRow(a, inner, focused && m.dialog == noDialog && i == cur, c))
	}
	return lines, hits
}

// jobListRows is how many jobs the jobs screen has room for, under their
// column labels and above the actions.
func (m cockpit) jobListRows() int {
	return max(0, m.paneRows()-2-1-(len(m.jobsActions())+2))
}

// jobsPane lists a project's jobs: each one's name, its schedule in words,
// how its last run went and when it runs next.
func (m cockpit) jobsPane(w, rows int) ([]string, []hit, []keyHelp) {
	inner := w - 4
	c := m.color
	title := m.shown + "'s jobs"
	pj := m.shownJobs()
	var lines []string
	var hits []hit
	para := func(s string) {
		for _, l := range strings.Split(ansi.Wrap(s, max(1, inner), ""), "\n") {
			lines = append(lines, sgr(l, fgSecondary, "", false, c))
		}
	}
	switch {
	case pj == nil:
		lines = append(lines, sgr("Loading "+m.shown+"'s jobs…", fgDim, "", false, c))
	case pj.unavailable != "":
		para(pj.unavailable)
	case len(pj.Jobs) == 0:
		para("No jobs yet. Add one in the Jobs tab of pier's dashboard.")
	default:
		lines, hits = m.jobRows(pj.Jobs, inner, w)
	}
	lines, ah := m.actionBlock(lines, m.jobsActions(), inner, w, m.jobsFocused() == actionsPane)
	return box(title, lines, w, rows, true, c), append(hits, ah...), jobsKeys
}

func (m cockpit) jobRows(list []jobSummary, inner, w int) ([]string, []hit) {
	c := m.color
	lastW, nextW := 13, 11
	nameW := 8
	for _, j := range list {
		nameW = max(nameW, ansi.StringWidth(j.Name)+2)
	}
	nameW = min(nameW, 24)
	schedW := inner - nameW - lastW - nextW
	if schedW < 10 {
		schedW = 0
		nameW = max(4, inner-lastW-nextW)
	}
	if nameW+schedW+lastW+nextW > inner {
		nextW = 0
		nameW = max(4, inner-lastW)
	}
	label := func(s string, n int) string { return sgr(fit(s, n), fgDim, "", false, c) }
	lines := []string{label("Job", nameW) + label("Schedule", schedW) + label("Last run", lastW) + label("Next run", nextW)}
	var hits []hit
	focused := m.jobsFocused() == projectPane && m.dialog == noDialog
	off := window(m.jCursor, m.jOff, len(list), m.jobListRows())
	for i := off; i < len(list) && i < off+m.jobListRows(); i++ {
		j := list[i]
		sel := focused && i == m.jCursor
		nameInk := fgBright
		if !j.Enabled {
			nameInk = fgSecondary
		}
		word := lastRunWord(j)
		row := sgr(fit(j.Name, nameW), nameInk, "", sel, c) + sgr(gapped(j.Description, schedW), fgSecondary, "", false, c) +
			pad(dotted(word, runInk(word), c), lastW) + sgr(fit(nextIn(j), nextW), fgDim, "", false, c)
		if sel {
			row = lit(row, inner, c)
		}
		hits = append(hits, hit{y: len(lines), x1: w, kind: hitJob, index: i})
		lines = append(lines, row)
	}
	return lines, hits
}

// gapped is text cut to a column of n cells that keeps two of them free,
// so it never runs into the next column.
func gapped(s string, n int) string {
	if n <= 2 {
		return fit("", n)
	}
	return fit(s, n-2) + "  "
}

// jobFacts are what the job screen says about a job before its runs: what
// it runs, when, whether it's on, and how long a command may take.
func jobFacts(j jobSummary) [][2]string {
	facts := [][2]string{{"Runs", j.what()}, {"Schedule", j.Description}}
	if j.Enabled {
		facts = append(facts, [2]string{"Enabled", "Yes"})
		if j.NextRun != nil {
			facts = append(facts, [2]string{"Next run", j.NextRun.Local().Format("Mon 2 Jan 15:04") + ", " + nextIn(j)})
		}
	} else {
		facts = append(facts, [2]string{"Enabled", "No, so it runs only when someone runs it"})
	}
	if j.Kind == "command" && j.TimeoutS > 0 {
		facts = append(facts, [2]string{"Timeout", fmt.Sprintf("%d minutes", j.TimeoutS/60)})
	}
	return facts
}

// runRows is how many runs the job screen has room for, under the job's
// facts, its actions, and the runs' label and column labels.
func (m cockpit) runRows() int {
	facts := 1
	if j, ok := m.openJob(); ok {
		facts = len(jobFacts(j))
	}
	return max(0, m.paneRows()-2-facts-(len(m.jobActions())+2)-3)
}

// jobPane is one job: what it runs and when, Run now, and its runs, newest
// first, loaded a page at a time as the cursor reaches the last of them.
func (m cockpit) jobPane(w, rows int) ([]string, []hit, []keyHelp) {
	inner := w - 4
	c := m.color
	j, ok := m.openJob()
	title := "Job"
	if ok {
		title = "Job " + j.Name
	}
	var lines []string
	var hits []hit
	if !ok {
		lines = append(lines, sgr("Loading…", fgDim, "", false, c))
	} else {
		labelW := 12
		for _, f := range jobFacts(j) {
			ink := fgSecondary
			if f[0] == "Runs" {
				ink = fgBright
			}
			lines = append(lines, sgr(fit(f[0], labelW), fgMuted, "", false, c)+sgr(ansi.Truncate(singleLine(f[1]), max(0, inner-labelW), "…"), ink, "", false, c))
		}
	}
	lines, ah := m.actionBlock(lines, m.jobActions(), inner, w, m.jobsFocused() == actionsPane)
	hits = append(hits, ah...)

	label := sgr("Runs", fgMuted, "", false, c)
	if m.runsTotal > len(m.jobRuns) {
		label += sgr(fmt.Sprintf("  %d of %d, more as you scroll", len(m.jobRuns), m.runsTotal), fgDim, "", false, c)
	}
	lines = append(lines, "", label)
	switch {
	case len(m.jobRuns) == 0 && m.loadingRuns:
		lines = append(lines, sgr("Loading…", fgDim, "", false, c))
	case len(m.jobRuns) == 0:
		lines = append(lines, sgr("No runs yet.", fgDim, "", false, c))
	default:
		rl, rh := m.runListRows(inner, w, len(lines))
		lines = append(lines, rl...)
		hits = append(hits, rh...)
	}
	return box(title, lines, w, rows, true, c), hits, jobsKeys
}

// runListRows are the runs' column labels and rows: when, how it started,
// how it went, its exit code or HTTP status, and how long it took. start
// is the row they begin on in the pane, for the hits.
func (m cockpit) runListRows(inner, w, start int) ([]string, []hit) {
	c := m.color
	whenW, resultW, codeW, tookW := 12, 13, 10, 8
	howW := inner - whenW - resultW - codeW - tookW
	if howW < 8 {
		howW, codeW = 0, 0
	}
	if whenW+howW+resultW+codeW+tookW > inner {
		tookW = max(0, inner-whenW-resultW)
	}
	label := func(s string, n int) string { return sgr(fit(s, n), fgDim, "", false, c) }
	lines := []string{label("When", whenW) + label("Started", howW) + label("Result", resultW) + label("Code", codeW) + label("Took", tookW)}
	var hits []hit
	focused := m.jobsFocused() == projectPane && m.dialog == noDialog
	n := m.runRows()
	off := window(m.rCursor, m.rOff, len(m.jobRuns), n)
	for i := off; i < len(m.jobRuns) && i < off+n; i++ {
		r := m.jobRuns[i]
		sel := focused && i == m.rCursor
		row := sgr(gapped(since(r.StartedAt), whenW), fgBright, "", sel, c) + sgr(gapped(startedHow(r), howW), fgSecondary, "", false, c) +
			pad(dotted(r.Status, runInk(r.Status), c), resultW) + sgr(gapped(runResult(r), codeW), fgSecondary, "", false, c) +
			sgr(fit(runTook(r), tookW), fgDim, "", false, c)
		if sel {
			row = lit(row, inner, c)
		}
		hits = append(hits, hit{y: start + len(lines), x1: w, kind: hitRun, index: i})
		lines = append(lines, row)
	}
	return lines, hits
}
