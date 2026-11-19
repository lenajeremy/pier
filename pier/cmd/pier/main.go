// Command pier is Pier's CLI. Deploys are a plain `git push pier <branch>`;
// this is for everything else.
//
//	pier login --server URL --token TOKEN
//	pier board
//	pier deployments PROJECT
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"
)

type config struct {
	Server string `json:"server"`
	Token  string `json:"token"`
}

func configPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = filepath.Join(os.Getenv("HOME"), ".config")
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		dir = xdg
	}
	return filepath.Join(dir, "pier", "config.json")
}

func loadConfig() config {
	var c config
	if b, err := os.ReadFile(configPath()); err == nil {
		json.Unmarshal(b, &c)
	}
	if v := os.Getenv("PIER_SERVER"); v != "" {
		c.Server = v
	}
	if v := os.Getenv("PIER_TOKEN"); v != "" {
		c.Token = v
	}
	if c.Server == "" {
		c.Server = "http://127.0.0.1:7777"
	}
	return c
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "login":
		err = login(os.Args[2:])
	case "board":
		err = showBoard()
	case "deployments", "ls":
		if len(os.Args) != 3 {
			err = errors.New("usage: pier deployments PROJECT")
		} else {
			err = showDeployments(os.Args[2])
		}
	case "help", "-h", "--help":
		usage()
		return
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "pier:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `Deploys are a git push:  git push pier <branch>

  pier login --server URL --token TOKEN   remember a server (token: pierd token)
  pier board                              every address on the server, and what it serves
  pier deployments PROJECT                a project's deployments, newest first
`)
}

func login(args []string) error {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	server := fs.String("server", "http://127.0.0.1:7777", "pierd's API address")
	tok := fs.String("token", "", "the token `pierd token` prints on the server")
	fs.Parse(args)
	if *tok == "" {
		return errors.New("pass --token; run `pierd token` on the server to see it")
	}
	c := config{Server: strings.TrimRight(*server, "/"), Token: *tok}
	var probe struct{ Domain string }
	if err := get(c, "/api/board", &probe); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(configPath()), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	if err := os.WriteFile(configPath(), b, 0o600); err != nil {
		return err
	}
	fmt.Printf("Logged in to %s (%s).\n", c.Server, probe.Domain)
	return nil
}

func get(c config, path string, into any) error {
	req, err := http.NewRequest(http.MethodGet, c.Server+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		var uerr *url.Error
		if errors.As(err, &uerr) {
			return fmt.Errorf("can't reach pierd at %s; is it running?", c.Server)
		}
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct{ Error string }
		if json.Unmarshal(body, &e) == nil && e.Error != "" {
			return errors.New(e.Error)
		}
		return fmt.Errorf("pierd answered %s", resp.Status)
	}
	return json.Unmarshal(body, into)
}

// colour paints a status the way the board does, on terminals that want it.
func colour(status string) string {
	word := strings.ToUpper(status)
	if os.Getenv("NO_COLOR") != "" {
		return word
	}
	if fi, err := os.Stdout.Stat(); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return word
	}
	rgb := map[string]string{
		"live": "244;156;203", "preview": "188;174;248", "building": "188;174;248",
		"ready": "98;216;198", "failed": "255;180;180",
	}[status]
	if rgb == "" {
		return word
	}
	return "\x1b[38;2;" + rgb + "m" + word + "\x1b[0m"
}

func showBoard() error {
	var b struct {
		Domain string
		Rows   []struct{ Address, Deployment, Branch, Status, URL string }
	}
	if err := get(loadConfig(), "/api/board", &b); err != nil {
		return err
	}
	if len(b.Rows) == 0 {
		fmt.Printf("Nothing on %s yet. Push a project to put it on the board:\n\n  git remote add pier ssh://git@<server>:2222/<project>\n  git push pier main\n", b.Domain)
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "ADDRESS\tDEPLOYMENT\tBRANCH\tSTATUS")
	for _, r := range b.Rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Address, r.Deployment, r.Branch, colour(r.Status))
	}
	return tw.Flush()
}

func showDeployments(project string) error {
	var res struct {
		Deployments []struct {
			ID, Branch, Message, Status, Framework string
			BuildMS                                int64     `json:"build_ms"`
			CreatedAt                              time.Time `json:"created_at"`
		}
	}
	if err := get(loadConfig(), "/api/projects/"+url.PathEscape(project)+"/deployments", &res); err != nil {
		return err
	}
	if len(res.Deployments) == 0 {
		fmt.Printf("%s has no deployments yet.\n", project)
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "DEPLOYMENT\tBRANCH\tSTATUS\tBUILD\tCREATED\tCOMMIT")
	for _, d := range res.Deployments {
		build := "-"
		if d.BuildMS > 0 {
			build = fmt.Sprintf("%.1fs", float64(d.BuildMS)/1000)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", d.ID, d.Branch, colour(d.Status), build, ago(d.CreatedAt), d.Message)
	}
	return tw.Flush()
}

func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hours ago", int(d.Hours()))
	default:
		return t.Format("2 Jan 2006")
	}
}
