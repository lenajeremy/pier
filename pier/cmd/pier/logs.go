package main

import (
	"flag"
	"fmt"
	"net/url"
	"pier/internal/apphost"
	"pier/internal/requests"
	"strconv"
	"time"
)

// logs prints a bounded initial window, then drains successive pages by ID.
// With --runtime it prints stdout/stderr from a server deployment's container.
func logs(args []string) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	project := fs.String("project", "", "project (defaults to the linked folder)")
	follow := fs.Bool("follow", false, "keep printing new lines")
	fs.BoolVar(follow, "f", false, "keep printing new lines")
	runtimeLogs := fs.Bool("runtime", false, "show container stdout/stderr instead of HTTP request logs")
	deployment := fs.String("deployment", "", "deployment ID for --runtime (defaults to production)")
	status := fs.String("status", "", "HTTP status, status class, or errors")
	path := fs.String("path", "", "match part of the request path")
	since := fs.Duration("since", 24*time.Hour, "initial time window")
	limit := fs.Int("limit", 100, "initial number of lines (1–500)")
	if err := fs.Parse(reorder(args, map[string]bool{"project": true, "deployment": true, "path": true, "status": true, "since": true, "limit": true})); err != nil {
		return err
	}
	if fs.NArg() > 1 || (fs.NArg() == 1 && *project != "") {
		return fmt.Errorf("specify one project")
	}
	if fs.NArg() == 1 {
		*project = fs.Arg(0)
	}
	if *limit < 1 || *limit > 500 || *since <= 0 {
		return fmt.Errorf("use a positive duration and a limit between 1 and 500")
	}
	c, name, err := projectFor(*project)
	if err != nil {
		return err
	}
	if *runtimeLogs {
		return printRuntimeLogs(c, name, *deployment, *limit, *follow)
	}
	if *status != "" && *status != "errors" && *status != "2xx" && *status != "3xx" && *status != "4xx" && *status != "5xx" {
		n, e := strconv.Atoi(*status)
		if e != nil || n < 100 || n > 599 {
			return fmt.Errorf("use an HTTP status, 2xx through 5xx, or errors")
		}
	}
	q := url.Values{"q": {*path}, "status": {*status}, "from": {strconv.FormatInt(time.Now().Add(-*since).UnixMilli(), 10)}, "limit": {strconv.Itoa(*limit)}}
	var cursor int64
	for {
		var res struct {
			Lines []requests.Line `json:"lines"`
		}
		if err := call(c, "GET", "/api/projects/"+url.PathEscape(name)+"/requests?"+q.Encode(), nil, &res); err != nil {
			return err
		}
		for _, line := range res.Lines {
			fmt.Printf("%s  %d  %s %q  %.1f ms  %s\n", time.UnixMilli(line.At).Format(time.RFC3339), line.Status, line.Method, line.Path, line.MS, line.Deployment)
			cursor = line.ID
		}
		if !*follow {
			return nil
		}
		q.Set("after", strconv.FormatInt(cursor, 10))
		q.Set("limit", "500")
		if len(res.Lines) < 500 {
			time.Sleep(time.Second)
		}
	}
}

func printRuntimeLogs(c config, project, depID string, tail int, follow bool) error {
	if depID == "" {
		var proj struct {
			Production *struct {
				Deployment *struct {
					ID      string `json:"id"`
					Runtime string `json:"runtime"`
				} `json:"deployment"`
			} `json:"production"`
			Deployments []struct {
				ID      string `json:"id"`
				Status  string `json:"status"`
				Runtime string `json:"runtime"`
			} `json:"deployments"`
		}
		if err := call(c, "GET", "/api/projects/"+url.PathEscape(project), nil, &proj); err != nil {
			return err
		}
		if proj.Production != nil && proj.Production.Deployment != nil && proj.Production.Deployment.Runtime != "" {
			depID = proj.Production.Deployment.ID
		} else {
			for _, d := range proj.Deployments {
				if d.Status == "ready" && d.Runtime != "" {
					depID = d.ID
					break
				}
			}
		}
		if depID == "" {
			return fmt.Errorf("%s has no running server deployment", project)
		}
	}
	endpoint := "/api/projects/" + url.PathEscape(project) + "/deployments/" + url.PathEscape(depID) + "/runtime-log"
	seenAt := ""
	seenAtCount := 0
	for {
		q := url.Values{"tail": {strconv.Itoa(tail)}}
		if seenAt != "" {
			q.Set("since", seenAt)
			q.Set("tail", "500")
		}
		var res struct {
			Lines []apphost.LogLine `json:"lines"`
		}
		if err := call(c, "GET", endpoint+"?"+q.Encode(), nil, &res); err != nil {
			return err
		}
		skip := seenAtCount
		for _, l := range res.Lines {
			if seenAt != "" && l.At != "" && l.At < seenAt {
				continue
			}
			if seenAt != "" && l.At == seenAt && skip > 0 {
				skip--
				continue
			}
			if l.At != "" {
				fmt.Printf("%s  %-6s  %s\n", l.At, l.Stream, l.Text)
			} else {
				fmt.Printf("%-6s  %s\n", l.Stream, l.Text)
			}
			if l.At != "" {
				if l.At == seenAt {
					seenAtCount++
				} else {
					seenAt = l.At
					seenAtCount = 1
				}
			}
		}
		if !follow {
			return nil
		}
		time.Sleep(1500 * time.Millisecond)
	}
}
