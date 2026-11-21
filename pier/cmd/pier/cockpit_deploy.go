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
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"pier/internal/bundle"
)

// pendingDeploy is a deploy the cockpit starts as it opens, for pier
// deploy on a terminal.
type pendingDeploy struct {
	project, dir, target string
	g                    git
	notes                []string
}

type deployEvent struct {
	phase string
	text  string // what the push step is doing, while it's still here
	row   *logRow
	url   string
	done  bool
	err   error
}

// The four steps a deploy goes through, as the deploy screen shows them.
const (
	stepPush = iota
	stepBuild
	stepDeploy
	stepLive
	stepCount
)

var stepNames = [stepCount]string{"Push", "Build", "Deploy", "Go live"}

// deployRun is one deploy followed from this folder. It outlives the
// deploy screen: Esc goes back to the projects while the build carries on,
// and d shows it again.
type deployRun struct {
	id                   int
	project, target, dir string
	g                    git
	started              time.Time
	in                   <-chan deployEvent
	cancel               context.CancelFunc
	rows                 []logRow
	step                 int // the step running now; stepCount once it's all done
	begun, ended         [stepCount]time.Time
	text                 [stepCount]string
	label                string // what the build found, such as vite 8.3
	liveURL              string
	deployment           string
	done, held           bool
	failed               int // the step that failed, or -1
	err                  error
}

// startDeploy packs the folder, uploads it and follows the narration in
// the background; the cockpit reads its events one at a time.
func (m *cockpit) startDeploy(p pendingDeploy) {
	ctx, events := m.startDeployState(p)
	go upload(ctx, m.c, p, m.run.started, events)
}

// startDeployState sets up the deploy screen for a new deploy, and hands
// back where its events go.
func (m *cockpit) startDeployState(p pendingDeploy) (context.Context, chan<- deployEvent) {
	if m.run != nil && m.run.cancel != nil && !m.run.done {
		m.run.cancel()
	}
	m.runs++
	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan deployEvent, 64)
	started := time.Now()
	r := &deployRun{
		id: m.runs, project: p.project, target: p.target, dir: p.dir, g: p.g,
		started: started, in: events, cancel: cancel, failed: -1,
	}
	r.begun[stepPush] = started
	r.text = [stepCount]string{
		"packing " + filepath.Base(p.dir),
		"in a throwaway container",
		"kept, never changed again",
		"it gets its own address",
	}
	if p.target == "production" {
		r.text[stepLive] = "production moves to it"
		if d := m.details[p.project]; d != nil && d.Address != "" {
			r.text[stepLive] = d.Address + " moves to it"
		}
	}
	m.run = r
	m.view = deployView
	m.actKeys[deployView] = ""
	m.deployErr = nil
	m.choose(p.project)
	return ctx, events
}

// upload sends the folder to pierd and passes on what it narrates, until
// the build ends or the cockpit stops listening.
func upload(ctx context.Context, c config, p pendingDeploy, started time.Time, events chan<- deployEvent) {
	defer close(events)
	emit := func(ev deployEvent) bool {
		select {
		case events <- ev:
			return true
		case <-ctx.Done():
			return false
		}
	}
	for _, note := range p.notes {
		row, _ := parseNarrationRow("pier    "+note, started)
		emit(deployEvent{phase: "note", row: &row})
	}
	files, err := bundle.Files(p.dir)
	if err != nil {
		emit(deployEvent{done: true, err: err})
		return
	}
	if len(files) == 0 {
		emit(deployEvent{done: true, err: fmt.Errorf("%s has nothing to upload", p.dir)})
		return
	}
	tmp, err := os.CreateTemp("", "pier-upload-*.tar.gz")
	if err != nil {
		emit(deployEvent{done: true, err: err})
		return
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	size, err := bundle.Pack(tmp, p.dir, files)
	if err != nil {
		emit(deployEvent{done: true, err: fmt.Errorf("packing %s: %w", p.dir, err)})
		return
	}
	packed, _ := tmp.Seek(0, 1)
	if _, err := tmp.Seek(0, 0); err != nil {
		emit(deployEvent{done: true, err: err})
		return
	}
	row, _ := parseNarrationRow(fmt.Sprintf("pier    uploading %s (%s, %s packed) to %s, for %s",
		plural(len(files), "file"), human(size), human(packed), p.project, p.target), started)
	emit(deployEvent{phase: "upload", text: "uploading " + plural(len(files), "file") + ", " + human(packed), row: &row})

	q := url.Values{
		"target": {p.target}, "files": {strconv.Itoa(len(files))},
		"branch": {p.g.branch}, "commit": {p.g.commit}, "message": {p.g.message},
		"tools": {localTools(p.dir)},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Server+"/api/projects/"+url.PathEscape(p.project)+"/deployments?"+q.Encode(), tmp)
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
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		r, foundURL := parseNarrationRow(sc.Text(), started)
		if !emit(deployEvent{row: &r, url: foundURL}) {
			return
		}
	}
	if err := sc.Err(); err != nil {
		emit(deployEvent{done: true, err: fmt.Errorf("lost the connection while it built; the build carries on: %w", err)})
		return
	}
	if resp.Trailer.Get("Pier-Result") != "ok" {
		emit(deployEvent{done: true, err: errors.New("the build failed; nothing changed")})
		return
	}
	emit(deployEvent{done: true})
}

func (m cockpit) readDeploy() tea.Cmd {
	r := m.run
	if r == nil {
		return nil
	}
	id, in := r.id, r.in
	return func() tea.Msg {
		ev, ok := <-in
		if !ok {
			ev = deployEvent{done: true}
		}
		return deployMsg{run: id, ev: ev}
	}
}

// onDeploy moves the steps along as the narration arrives: the server's
// first line means the upload is in, a deploy line that the build is
// done, an alias line that the address points at it.
func (m *cockpit) onDeploy(ev deployEvent) tea.Cmd {
	r := m.run
	now := time.Now()
	if ev.text != "" && r.step == stepPush {
		r.text[stepPush] = ev.text
	}
	if ev.row != nil {
		r.rows = append(r.rows, *ev.row)
		if len(r.rows) > logHistory {
			r.rows = r.rows[len(r.rows)-logHistory:]
		}
		if ev.phase == "" {
			r.narrate(*ev.row, ev.url, now)
		}
	}
	if !ev.done {
		return m.readDeploy()
	}
	r.done = true
	r.err = ev.err
	// What can be done changes once it's over; the first of it gets the
	// focus, such as opening the address it went live at.
	m.actKeys[deployView] = ""
	if ev.err != nil {
		if r.failed < 0 {
			r.failed = min(r.step, stepLive)
			row, _ := parseNarrationRow("pier    "+ev.err.Error(), r.started)
			r.rows = append(r.rows, row)
		}
		if r.begun[r.failed].IsZero() {
			r.begun[r.failed] = now
		}
		r.ended[r.failed] = now
		if m.ranDeploy {
			m.deployErr = ev.err
		}
		if m.view != deployView {
			m.say("The deploy of "+r.project+" failed. Press d to see why.", coralTxt)
		}
	} else {
		for s := r.step; s < stepCount; s++ {
			r.reach(s, now)
			r.ended[s] = now
		}
		r.step = stepCount
		if r.liveURL != "" {
			r.text[stepLive] = "live at " + host(r.liveURL)
		}
		if r.held {
			r.text[stepLive] = "held back: production is rolled back"
		}
		if m.ranDeploy {
			m.deployErr = nil
		}
		if m.view != deployView {
			m.say("✓ "+r.project+" is live at "+host(r.liveURL), mint)
		}
	}
	return m.kick()
}

// reach starts step s, finishing every step before it.
func (r *deployRun) reach(s int, now time.Time) {
	for r.step < s {
		if r.begun[r.step].IsZero() {
			r.begun[r.step] = now
		}
		r.ended[r.step] = now
		r.step++
	}
	if r.step < stepCount && r.begun[r.step].IsZero() {
		r.begun[r.step] = now
	}
}

func (r *deployRun) narrate(row logRow, foundURL string, now time.Time) {
	msg := row.message
	if r.step == stepPush {
		r.reach(stepBuild, now)
		switch {
		case r.g.commit != "":
			r.text[stepPush] = "received " + r.g.commit[:min(7, len(r.g.commit))] + " from this folder"
		default:
			r.text[stepPush] = "received this folder"
		}
	}
	switch row.label {
	case "build":
		if label, _, ok := strings.Cut(msg, ", found in "); ok {
			r.label = label
			r.text[stepBuild] = label
		} else if runner, ok := strings.CutPrefix(msg, "running in "); ok && r.label != "" {
			r.text[stepBuild] = r.label + " in " + runner
		} else if image, ok := strings.CutPrefix(msg, "building a runnable image with "); ok {
			r.text[stepBuild] = "a runnable image with " + image
		} else if strings.HasPrefix(msg, "using Railpack") && r.label != "" {
			r.text[stepBuild] = r.label + ", built with Railpack"
		}
	case "deploy":
		r.reach(stepDeploy, now)
		if rest, ok := strings.CutPrefix(msg, "kept as "); ok {
			id, _, _ := strings.Cut(rest, ":")
			r.deployment = id
			r.text[stepDeploy] = "kept as " + id + ", never changed again"
		} else if rest, ok := strings.CutPrefix(msg, "application "); ok {
			id, _, _ := strings.Cut(rest, " ")
			r.deployment = id
			r.text[stepDeploy] = id + " passed its startup check"
		} else if strings.HasPrefix(msg, "starting the process") {
			r.text[stepDeploy] = "starting it and checking that it answers"
		}
	case "alias":
		if foundURL != "" {
			r.reach(stepLive, now)
			if r.liveURL == "" {
				r.liveURL = foundURL
				r.text[stepLive] = "live at " + host(foundURL)
			}
		}
	case "pier":
		switch {
		case strings.HasPrefix(msg, "build failed after"), strings.HasPrefix(msg, "can't open this project's variables"):
			r.failed = r.step
		case strings.HasPrefix(msg, "production is rolled back"):
			r.held = true
		}
	}
}

// stepState says how a step stands: waiting, running, done or failed.
func (r *deployRun) stepState(s int) string {
	switch {
	case r.failed == s:
		return "failed"
	case r.failed >= 0 && s > r.failed:
		return "waiting"
	case s < r.step:
		return "done"
	case s == r.step && !r.done:
		return "running"
	}
	return "waiting"
}

// stepTime is how long a step took, or has taken so far.
func (r *deployRun) stepTime(s int) string {
	if r.begun[s].IsZero() {
		return ""
	}
	end := r.ended[s]
	if end.IsZero() {
		if r.done {
			return ""
		}
		end = time.Now()
	}
	return seconds(end.Sub(r.begun[s]))
}

func seconds(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}
