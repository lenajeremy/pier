package main

import (
	"flag"
	"fmt"
	"net/url"
	"pier/internal/requests"
	"strconv"
	"time"
)

// logs prints a bounded initial window, then drains successive pages by ID.
func logs(args []string) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	project := fs.String("project", "", "project (defaults to the linked folder)")
	follow := fs.Bool("follow", false, "keep printing new requests")
	fs.BoolVar(follow, "f", false, "keep printing new requests")
	status := fs.String("status", "", "HTTP status, status class, or errors")
	path := fs.String("path", "", "match part of the request path")
	since := fs.Duration("since", 24*time.Hour, "initial time window")
	limit := fs.Int("limit", 100, "initial number of lines (1–500)")
	if err := fs.Parse(reorder(args, map[string]bool{"project": true, "path": true, "status": true, "since": true, "limit": true})); err != nil {
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
	if *status != "" && *status != "errors" && *status != "2xx" && *status != "3xx" && *status != "4xx" && *status != "5xx" {
		n, e := strconv.Atoi(*status)
		if e != nil || n < 100 || n > 599 {
			return fmt.Errorf("use an HTTP status, 2xx through 5xx, or errors")
		}
	}
	c, name, err := projectFor(*project)
	if err != nil {
		return err
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
