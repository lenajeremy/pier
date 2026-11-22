package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

// jobSummary is a scheduled job as pierd lists it.
type jobSummary struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	Kind        string     `json:"kind"`
	Command     string     `json:"command"`
	Method      string     `json:"method"`
	Path        string     `json:"path"`
	Enabled     bool       `json:"enabled"`
	Description string     `json:"description"`
	NextRun     *time.Time `json:"next_run"`
	LastRun     *jobRun    `json:"last_run"`
	Running     bool       `json:"running"`
}

type jobRun struct {
	ID         int64     `json:"id"`
	Status     string    `json:"status"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	ExitCode   *int      `json:"exit_code"`
	HTTPStatus *int      `json:"http_status"`
	Detail     string    `json:"detail"`
}

func (j jobSummary) what() string {
	if j.Kind == "command" {
		return j.Command
	}
	return j.Method + " " + j.Path
}

const jobsHelp = `Scheduled jobs run a command in a fresh container from production's image,
or call a path on production, on a cron schedule set in the dashboard's Jobs tab.

  pier jobs [PROJECT]                 list a project's jobs: schedule, last run, next run
  pier jobs run NAME [--project P]    run a job now and print its output as it comes
                                      (exits 1 if the run fails; Ctrl-C stops following,
                                      not the run)

PROJECT defaults to the one this folder is linked to.
`

// jobsCmd is pier jobs: a project's jobs, or with run, one run of a job.
func jobsCmd(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help", "help":
			fmt.Print(jobsHelp)
			return nil
		case "run":
			return runJob(args[1:])
		}
	}
	fs := flag.NewFlagSet("jobs", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, jobsHelp) }
	projectFlag := fs.String("project", "", "the project (default: this folder's)")
	fs.Parse(reorder(args, map[string]bool{"project": true}))
	if fs.NArg() > 1 || (fs.NArg() == 1 && *projectFlag != "") {
		return errors.New("say one project: pier jobs [PROJECT]")
	}
	if fs.NArg() == 1 {
		*projectFlag = fs.Arg(0)
	}
	c, project, err := projectFor(*projectFlag)
	if err != nil {
		return err
	}
	list, err := listJobs(c, project)
	if err != nil {
		return err
	}
	if term() {
		heading("Scheduled jobs", project)
	}
	if len(list) == 0 {
		fmt.Printf("%s has no scheduled jobs. Add one in the dashboard's Jobs tab.\n", project)
		return nil
	}
	if term() {
		for _, j := range list {
			detail := j.Description + ", " + nextRun(j)
			if j.LastRun != nil {
				detail += ", last run " + ago(j.LastRun.StartedAt)
			}
			resultRow(j.Name+"  "+j.what(), lastStatus(j), detail)
		}
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "NAME\tCOMMAND OR PATH\tSCHEDULE\tLAST RUN\tNEXT RUN")
	for _, j := range list {
		last := "-"
		if j.LastRun != nil {
			last = lastStatus(j) + ", " + ago(j.LastRun.StartedAt)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", j.Name, j.what(), j.Description, last, nextRun(j))
	}
	return tw.Flush()
}

func listJobs(c config, project string) ([]jobSummary, error) {
	var res struct{ Jobs []jobSummary }
	if err := callWith("Loading "+project+"'s jobs", c, http.MethodGet, "/api/projects/"+url.PathEscape(project)+"/jobs", nil, &res); err != nil {
		return nil, err
	}
	return res.Jobs, nil
}

func lastStatus(j jobSummary) string {
	switch {
	case j.Running:
		return "running"
	case j.LastRun != nil:
		return j.LastRun.Status
	case !j.Enabled:
		return "off"
	}
	return "not run"
}

func nextRun(j jobSummary) string {
	switch {
	case !j.Enabled:
		return "off"
	case j.NextRun == nil:
		return "never"
	}
	d := time.Until(*j.NextRun)
	switch {
	case d < time.Minute:
		return "next within a minute"
	case d < time.Hour:
		return fmt.Sprintf("next in %d min", int(d.Minutes()+0.5))
	case d < 24*time.Hour:
		return fmt.Sprintf("next in %d hours", int(d.Hours()+0.5))
	}
	return "next " + j.NextRun.Local().Format("Mon 2 Jan 15:04")
}

// runJob is pier jobs run NAME: it starts a run now and prints its output
// until it ends. The run goes on if pier stops following it.
func runJob(args []string) error {
	fs := flag.NewFlagSet("jobs run", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, jobsHelp) }
	projectFlag := fs.String("project", "", "the project (default: this folder's)")
	fs.Parse(reorder(args, map[string]bool{"project": true}))
	if fs.NArg() != 1 {
		return errors.New("say which job: pier jobs run NAME (pier jobs lists them)")
	}
	name := fs.Arg(0)
	c, project, err := projectFor(*projectFlag)
	if err != nil {
		return err
	}
	if err := mayChange(c, "run jobs"); err != nil {
		return err
	}
	list, err := listJobs(c, project)
	if err != nil {
		return err
	}
	var job *jobSummary
	var names []string
	for i := range list {
		names = append(names, list[i].Name)
		if list[i].Name == name {
			job = &list[i]
		}
	}
	if job == nil {
		if len(names) == 0 {
			return fmt.Errorf("%s has no jobs; add one in the dashboard's Jobs tab", project)
		}
		return fmt.Errorf("%s has no job called %s; it has %s", project, name, strings.Join(names, ", "))
	}
	base := "/api/projects/" + url.PathEscape(project) + "/jobs/" + strconv.FormatInt(job.ID, 10)
	var started struct{ Run int64 }
	if err := callWith("Starting "+name, c, http.MethodPost, base+"/run", nil, &started); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Started run %d of %s: %s\n", started.Run, name, job.what())
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	from := 0
	for {
		var res struct {
			Run   jobRun
			Lines []struct{ Stream, Text string }
			Next  int
			Done  bool
		}
		err := callContext(ctx, c, http.MethodGet, base+"/runs/"+strconv.FormatInt(started.Run, 10)+"?from="+strconv.Itoa(from), nil, &res)
		if ctx.Err() != nil {
			fmt.Fprintf(os.Stderr, "\nStopped following. The run goes on; pier jobs %s shows how it ended.\n", project)
			return errCancelled
		}
		if err != nil {
			return err
		}
		for _, l := range res.Lines {
			fmt.Printf("%-7s %s\n", l.Stream, l.Text)
		}
		from = res.Next
		if res.Done {
			r := res.Run
			result := ""
			switch {
			case r.HTTPStatus != nil:
				result = fmt.Sprintf(", HTTP %d", *r.HTTPStatus)
			case r.ExitCode != nil:
				result = fmt.Sprintf(", exit code %d", *r.ExitCode)
			}
			took := r.FinishedAt.Sub(r.StartedAt).Round(100 * time.Millisecond).String()
			if d := r.FinishedAt.Sub(r.StartedAt); d < time.Second {
				took = strconv.FormatInt(d.Milliseconds(), 10) + " ms"
			}
			if r.Status == "succeeded" {
				fmt.Fprintf(os.Stderr, "%s %s succeeded in %s%s.\n", tick(), name, took, result)
				return nil
			}
			return fmt.Errorf("%s %s after %s%s: %s", name, r.Status, took, result, r.Detail)
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
}
