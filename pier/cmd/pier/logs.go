package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"pier/internal/apphost"
	"pier/internal/requests"
)

type logSource struct {
	c                         config
	project, kind, deployment string
	job, run                  int64  // a job's run, for kind Run
	jobName                   string // the job's name, for the title
	query                     url.Values
	limit                     int
	follow                    bool
}

func logs(args []string) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	project := fs.String("project", "", "project (defaults to the linked folder)")
	follow := fs.Bool("follow", interactive(), "keep printing new lines (default in a terminal)")
	fs.BoolVar(follow, "f", *follow, "keep printing new lines")
	once := fs.Bool("once", false, "print a single snapshot")
	runtimeLogs := fs.Bool("runtime", false, "show container stdout/stderr")
	build := fs.Bool("build", false, "show build output")
	deployment := fs.String("deployment", "", "deployment ID for runtime or build logs")
	status := fs.String("status", "", "HTTP status, status class, or errors")
	path := fs.String("path", "", "match part of the request path")
	since := fs.Duration("since", 24*time.Hour, "initial request time window")
	limit := fs.Int("limit", 100, "initial number of lines (1–500)")
	if err := fs.Parse(reorder(args, map[string]bool{"project": true, "deployment": true, "path": true, "status": true, "since": true, "limit": true})); err != nil {
		return err
	}
	if fs.NArg() > 1 || (fs.NArg() == 1 && *project != "") {
		return errors.New("specify one project")
	}
	if fs.NArg() == 1 {
		*project = fs.Arg(0)
	}
	if *limit < 1 || *limit > 500 || *since <= 0 {
		return errors.New("use a positive duration and a limit between 1 and 500")
	}
	if *runtimeLogs && *build {
		return errors.New("choose either --runtime or --build")
	}
	if *deployment != "" && !*runtimeLogs && !*build {
		return errors.New("--deployment requires --runtime or --build")
	}
	if *status != "" && *status != "errors" && *status != "2xx" && *status != "3xx" && *status != "4xx" && *status != "5xx" {
		n, err := strconv.Atoi(*status)
		if err != nil || n < 100 || n > 599 {
			return errors.New("use an HTTP status, 2xx through 5xx, or errors")
		}
	}
	c, name, err := projectFor(*project)
	if err != nil {
		return err
	}
	s := logSource{c: c, project: name, kind: "Request", deployment: *deployment, limit: *limit, follow: *follow && !*once,
		query: url.Values{"q": {*path}, "status": {*status}, "from": {strconv.FormatInt(time.Now().Add(-*since).UnixMilli(), 10)}}}
	if *runtimeLogs {
		s.kind = "Runtime"
	}
	if *build {
		s.kind = "Build"
	}
	if interactive() && s.follow {
		return openLogView(s)
	}
	styled := term()
	color := styled && os.Getenv("NO_COLOR") == ""
	return s.read(context.Background(), func(batch []logRow) error {
		for _, line := range batch {
			if _, err := fmt.Fprintln(os.Stdout, line.format(styled, color)); err != nil {
				return err
			}
		}
		return nil
	})
}

// A follow response is a sequence of flushed JSON pages. Older servers
// answer the same URL with one JSON page, which the caller follows by cursor.
func readLogPages[T any](ctx context.Context, s logSource, path string, consume func([]T) error) (streamed bool, err error) {
	resp, err := request(ctx, s.c, "GET", path, nil)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	streamed = strings.HasPrefix(resp.Header.Get("Content-Type"), "application/x-ndjson")
	decoder := json.NewDecoder(resp.Body)
	for {
		var page struct {
			Lines []T
			Error string
			Done  bool
		}
		if err := decoder.Decode(&page); err != nil {
			if err == io.EOF && streamed {
				return true, errors.New("log connection closed unexpectedly")
			}
			return streamed, err
		}
		if page.Error != "" {
			return streamed, errors.New(page.Error)
		}
		if err := consume(page.Lines); err != nil {
			return streamed, err
		}
		if !streamed || page.Done {
			return streamed || page.Done, nil
		}
	}
}

func waitForLogs(ctx context.Context) error {
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s logSource) read(ctx context.Context, emit func([]logRow) error) error {
	if s.kind == "Run" {
		return s.readRun(ctx, emit)
	}
	base := "/api/projects/" + url.PathEscape(s.project)
	q := url.Values{}
	if s.follow {
		q.Set("follow", "1")
	}
	if s.kind == "Request" {
		for key, v := range s.query {
			q[key] = v
		}
		q.Set("limit", strconv.Itoa(s.limit))
		for {
			count := 0
			streamed, err := readLogPages(ctx, s, base+"/requests?"+q.Encode(), func(lines []requests.Line) error {
				rows := make([]logRow, 0, len(lines))
				for _, l := range lines {
					rows = append(rows, requestRow(l))
					q.Set("after", strconv.FormatInt(l.ID, 10))
				}
				count = len(lines)
				return emit(rows)
			})
			if err != nil || !s.follow || streamed {
				return err
			}
			if !q.Has("after") {
				q.Set("after", "0")
			}
			q.Set("limit", "500")
			if count < 500 {
				if err := waitForLogs(ctx); err != nil {
					return err
				}
			}
		}
	}
	id, err := s.resolveDeployment(ctx, base)
	if err != nil {
		return err
	}
	base += "/deployments/" + url.PathEscape(id)
	if s.kind == "Build" {
		from := 0
		first := true
		q.Set("tail", strconv.Itoa(s.limit))
		for {
			q.Set("from", strconv.Itoa(from))
			done, err := readLogPages(ctx, s, base+"/log?"+q.Encode(), func(lines []buildLine) error {
				from += len(lines)
				if first && len(lines) > s.limit {
					lines = lines[len(lines)-s.limit:]
				}
				first = false
				rows := make([]logRow, 0, len(lines))
				for _, l := range lines {
					rows = append(rows, buildRow(l))
				}
				return emit(rows)
			})
			if err != nil || done || !s.follow {
				return err
			}
			if err := waitForLogs(ctx); err != nil {
				return err
			}
		}
	}
	q.Set("tail", strconv.Itoa(s.limit))
	var last string
	var duplicates int
	for {
		skip := duplicates
		streamed, err := readLogPages(ctx, s, base+"/runtime-log?"+q.Encode(), func(lines []apphost.LogLine) error {
			rows := make([]logRow, 0, len(lines))
			for _, l := range lines {
				if q.Has("since") && l.At == q.Get("since") && skip > 0 {
					skip--
					continue
				}
				rows = append(rows, runtimeRow(l))
				if l.At != "" {
					if last != l.At {
						duplicates = 0
					}
					last = l.At
					duplicates++
				}
			}
			return emit(rows)
		})
		if err != nil || !s.follow || streamed {
			return err
		}
		if last != "" {
			q.Set("since", last)
		}
		q.Set("tail", "500")
		if err := waitForLogs(ctx); err != nil {
			return err
		}
	}
}

func (s logSource) resolveDeployment(ctx context.Context, base string) (string, error) {
	if s.deployment != "" {
		return s.deployment, nil
	}
	type dep struct{ ID, Status, Runtime string }
	var p struct {
		Production  *struct{ Deployment *dep }
		Deployments []dep
	}
	if err := callContext(ctx, s.c, "GET", base, nil, &p); err != nil {
		return "", err
	}
	if s.kind == "Build" {
		if len(p.Deployments) > 0 {
			return p.Deployments[0].ID, nil
		}
		return "", fmt.Errorf("%s has no deployments yet", s.project)
	}
	if p.Production != nil && p.Production.Deployment != nil && p.Production.Deployment.Runtime != "" {
		return p.Production.Deployment.ID, nil
	}
	for _, d := range p.Deployments {
		if d.Status == "ready" && d.Runtime != "" {
			return d.ID, nil
		}
	}
	return "", fmt.Errorf("%s has no running server deployment", s.project)
}

// runLine is one line of a job's run as pierd keeps it.
type runLine struct {
	MS     int64  `json:"ms"`
	Stream string `json:"stream"`
	Text   string `json:"text"`
}

// readRun reads a job's run from the start, asking for what's new every
// second while it runs, and ends with how it went.
func (s logSource) readRun(ctx context.Context, emit func([]logRow) error) error {
	path := fmt.Sprintf("%s/%d/runs/%d", jobsPath(s.project), s.job, s.run)
	from := 0
	said := "" // pierd's last words in the log, which the end needn't repeat
	for {
		var res struct {
			Run   jobRun
			Lines []runLine
			Next  int
			Done  bool
		}
		if err := callContext(ctx, s.c, "GET", path+"?from="+strconv.Itoa(from), nil, &res); err != nil {
			return err
		}
		rows := make([]logRow, 0, len(res.Lines)+1)
		for _, l := range res.Lines {
			rows = append(rows, runRow(l))
			if l.Stream == "pier" {
				said = cleanLog(l.Text)
			}
		}
		from = res.Next
		if res.Done {
			rows = append(rows, runEnd(res.Run, said))
		}
		if err := emit(rows); err != nil {
			return err
		}
		if res.Done {
			return nil
		}
		if err := waitForLogs(ctx); err != nil {
			return err
		}
	}
}

// runRow is a line of a run coloured the way the job page colours it:
// Pier's own words in violet, the run's output in pink, and what it wrote
// to stderr in coral.
func runRow(l runLine) logRow {
	stamp := fmt.Sprintf("+%.2fs", float64(l.MS)/1000)
	r := logRow{stamp: stamp, clock: stamp, label: cleanLog(l.Stream), message: cleanLog(l.Text), tone: pink}
	switch l.Stream {
	case "pier":
		r.tone = lavender
	case "stderr":
		r.tone, r.ink = coral, coralTxt
	}
	return r
}

// runEnd is the last line of a run's log: how it went, how long it took,
// its exit code or HTTP status, and why it failed or was skipped, unless
// pierd's last line in the log said why already.
func runEnd(r jobRun, said string) logRow {
	row := logRow{label: "pier", tone: lavender}
	switch r.Status {
	case "succeeded":
		row.message, row.ink = "Succeeded in "+runTook(r), mint
	case "skipped":
		row.message, row.ink = "Skipped", asleep
	default:
		row.message, row.ink = "Failed after "+runTook(r), coralTxt
	}
	if res := runResult(r); res != "" {
		row.message += ", " + res
	}
	if r.Detail != "" && r.Status != "succeeded" && cleanLog(r.Detail) != said {
		row.message += ": " + cleanLog(r.Detail)
	}
	return row
}
