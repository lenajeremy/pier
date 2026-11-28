// Command pier is Pier's CLI: sign this computer in, deploy a folder, and
// see what's served.
//
//	pier login ADDRESS              approve this computer in the dashboard
//	pier deploy [FOLDER] [--prod]   upload a folder and build it
//	pier rollback [DEPLOYMENT]      production goes back (--undo to undo)
//	pier promote DEPLOYMENT         production serves an existing deployment
//	pier board                      every address and what it serves
//	pier deployments [PROJECT]      a project's deployments, newest first
//	pier jobs [PROJECT]             a project's scheduled jobs (run NAME runs one now)
//	pier logout                     forget this computer's sign-in
//	pier version                    this pier's release, and its server's
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	terminal "golang.org/x/term"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/tabwriter"
	"time"

	"pier/internal/version"
)

type config struct {
	Server string `json:"server"`
	Token  string `json:"token"`
	Email  string `json:"email,omitempty"`
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
	return c
}

func saveConfig(c config) error {
	if err := os.MkdirAll(filepath.Dir(configPath()), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(configPath(), append(b, '\n'), 0o600)
}

func main() {
	if len(os.Args) < 2 {
		if interactive() {
			if err := home(); err != nil {
				fmt.Fprintln(os.Stderr, "pier:", err)
				os.Exit(1)
			}
			return
		}
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "login":
		err = login(os.Args[2:])
	case "logout":
		err = logout()
	case "deploy":
		err = deploy(os.Args[2:])
	case "logs":
		err = logs(os.Args[2:])
	case "rollback":
		err = rollback(os.Args[2:])
	case "promote":
		err = promote(os.Args[2:])
	case "restart":
		err = restart(os.Args[2:])
	case "board":
		err = showBoard(os.Args[2:])
	case "deployments", "ls":
		err = showDeployments(os.Args[2:])
	case "jobs":
		err = jobsCmd(os.Args[2:])
	case "db":
		err = dbCmd(os.Args[2:])
	case "storage":
		err = storageCmd(os.Args[2:])
	case "env":
		err = envCmd(os.Args[2:])
	case "docs":
		err = docsCmd(os.Args[2:])
	case "version":
		err = showVersion(os.Args[2:])
	case "upgrade":
		err = upgradeCmd(os.Args[2:])
	case "--version", "-v":
		fmt.Println("pier " + version.String())
		return
	case "help", "-h", "--help":
		usage()
		return
	default:
		err = fmt.Errorf("unknown command %q; run pier help", os.Args[1])
	}
	if err != nil {
		if errors.Is(err, errCancelled) {
			return
		}
		fmt.Fprintln(os.Stderr, "pier:", err)
		os.Exit(1)
	}
}

type commandGroup struct {
	title string
	rows  [][2]string
}

func commandReferenceGroups() []commandGroup {
	return []commandGroup{
		{"Interactive", [][2]string{
			{"pier", "Your projects, their deployments and the board"},
		}},
		{"Deploy", [][2]string{
			{"pier deploy [FOLDER] [--prod]", "Build a preview or deploy to production"},
			{"pier deploy --project P --create", "Make the project if it's new, then deploy"},
			{"pier promote [DEPLOYMENT]", "Move production to a ready deployment"},
			{"pier rollback [ID] [--undo]", "Roll back production or undo a rollback"},
			{"pier restart [PROJECT]", "Restart a server container without rebuilding"},
		}},
		{"Inspect", [][2]string{
			{"pier board [--plain]", "Every address on the server (or a plain table)"},
			{"pier deployments [PROJECT]", "A project's deployments, newest first"},
			{"pier logs [PROJECT]", "Follow requests (one snapshot when piped)"},
			{"pier logs [PROJECT] --runtime", "Follow container stdout and stderr"},
			{"pier logs [PROJECT] --build", "Follow a build (--deployment ID to choose)"},
			{"pier logs --once | --follow", "Choose a snapshot or continuous output"},
		}},
		{"Variables", [][2]string{
			{"pier env [PROJECT]", "A project's variables and where they reach"},
			{"pier env set NAME=VALUE...", "Set variables (--env production,preview)"},
			{"pier env rm NAME...", "Remove variables"},
		}},
		{"Database", [][2]string{
			{"pier db [PROJECT]", "The project's Postgres and its databases"},
			{"pier db create [PROJECT]", "Give the project Postgres"},
			{"pier db add NAME [PROJECT]", "Add a database, linked to the environments you say"},
			{"pier db external [PROJECT]", "Make a URL for a service outside Pier"},
			{"pier db link NAME [PROJECT]", "Change what a database serves (--env, --var)"},
			{"pier db tunnel [PROJECT]", "Reach a database from this computer"},
			{"pier db psql [PROJECT]", "Open psql on a database"},
		}},
		{"Storage", [][2]string{
			{"pier storage [PROJECT]", "The project's buckets on Cloudflare R2"},
			{"pier storage add NAME [PROJECT]", "Add a bucket (--public, --env, --prefix)"},
			{"pier storage env NAME [PROJECT]", "Print a bucket's variables for a .env file"},
			{"pier storage ls | put | get", "List, upload and download files"},
		}},
		{"Jobs", [][2]string{
			{"pier jobs [PROJECT]", "A project's scheduled jobs and their last runs"},
			{"pier jobs run NAME", "Run a job now and print its output"},
		}},
		{"Help", [][2]string{
			{"pier docs", "How Pier works, for people and coding agents"},
		}},
		{"Account", [][2]string{
			{"pier login [ADDRESS]", "Sign in through your dashboard"},
			{"pier logout", "Sign this computer out"},
			{"pier version", "Show pier's and pierd's versions"},
			{"pier upgrade", "Replace pier with the latest release"},
			{"pier upgrade --server", "Update pierd on the server (the owner only)"},
		}},
	}
}

func usage() {
	tty := terminal.IsTerminal(int(os.Stderr.Fd())) && os.Getenv("TERM") != "dumb"
	color := tty && os.Getenv("NO_COLOR") == ""
	if tty {
		w, _, _ := terminal.GetSize(int(os.Stderr.Fd()))
		fmt.Fprintln(os.Stderr)
		// Without a card under it, the brand's release ends where the
		// descriptions below do.
		fmt.Fprintln(os.Stderr, brand(min(w, 76), color))
		fmt.Fprintln(os.Stderr)
	}
	groups := commandReferenceGroups()
	if !tty {
		fmt.Fprintln(os.Stderr, "Pier CLI")
	}
	for _, g := range groups {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "  "+boldInk(strings.ToUpper(g.title), fgDim, color))
		for _, r := range g.rows {
			cmd := fmt.Sprintf("%-32s", r[0])
			fmt.Fprintf(os.Stderr, "  %s  %s\n", boldInk(cmd, fgBright, color), ink(r[1], fgMuted, color))
		}
	}
	fmt.Fprintln(os.Stderr)
}

// serverURL turns what the owner typed into Pier's address: HTTPS for a
// domain or IP, plain HTTP for this machine during development.
func serverURL(s string) (string, error) {
	s = strings.TrimRight(strings.TrimSpace(s), "/")
	if s == "" {
		return "", errors.New("say which Pier: pier login pier.example.com")
	}
	if !strings.Contains(s, "://") {
		host := s
		if h, _, err := net.SplitHostPort(s); err == nil {
			host = h
		}
		if host == "localhost" || strings.HasSuffix(host, ".localhost") || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback() {
			s = "http://" + s
		} else {
			s = "https://" + s
		}
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("%q isn't an address", s)
	}
	return u.Scheme + "://" + u.Host, nil
}

var client = &http.Client{Transport: &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
	TLSHandshakeTimeout:   10 * time.Second,
	ResponseHeaderTimeout: 10 * time.Minute, // an upload's build answers as it goes
}}

// call sends a JSON request and decodes a JSON answer.
func call(c config, method, path string, body, into any) error {
	return callContext(context.Background(), c, method, path, body, into)
}

func callContext(ctx context.Context, c config, method, path string, body, into any) error {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = strings.NewReader(string(b))
	}
	resp, err := request(ctx, c, method, path, r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if into == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(into)
}

func request(ctx context.Context, c config, method, path string, r io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.Server+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := client.Do(req)
	if err != nil {
		var uerr *url.Error
		if errors.As(err, &uerr) {
			return nil, fmt.Errorf("can't reach Pier at %s; is the address right, and is pierd running?", c.Server)
		}
		return nil, err
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		var e struct{ Error string }
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			if resp.StatusCode == http.StatusUnauthorized {
				return nil, fmt.Errorf("%s (pier login %s)", e.Error, strings.TrimPrefix(strings.TrimPrefix(c.Server, "https://"), "http://"))
			}
			return nil, errors.New(e.Error)
		}
		return nil, fmt.Errorf("Pier answered %s", resp.Status)
	}
	return resp, nil
}

func signedIn() (config, error) {
	c := loadConfig()
	if c.Server == "" || c.Token == "" {
		return c, errors.New("sign this computer in first: pier login <Pier's address>")
	}
	return c, nil
}

// ---- login ------------------------------------------------------------------

func login(args []string) error {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	server := fs.String("server", "", "Pier's address (or give it as the argument)")
	tok := fs.String("token", "", "sign in with the token `pierd token` prints, instead of approving")
	fs.Parse(reorder(args, map[string]bool{"server": true, "token": true}))
	addr := *server
	if addr == "" && fs.NArg() > 0 {
		addr = fs.Arg(0)
	}
	if addr == "" && interactive() {
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
			return err
		}
		if !ok {
			return errCancelled
		}
		addr = entered
	}
	base, err := serverURL(addr)
	if err != nil {
		return err
	}
	if *tok != "" { // the server's own token, from slice 1
		c := config{Server: base, Token: *tok}
		var probe struct{ Domain string }
		if err := call(c, http.MethodGet, "/api/board", nil, &probe); err != nil {
			return err
		}
		if err := saveConfig(c); err != nil {
			return err
		}
		fmt.Printf("Signed in to %s with the server's token.\n", host(base))
		return nil
	}
	if interactive() {
		cfg, err := openLoginView(base)
		if err != nil {
			return err
		}
		fmt.Printf("%s Signed in to %s as %s.\n", tick(), host(cfg.Server), cfg.Email)
		return nil
	}

	name, _ := os.Hostname()
	name = strings.TrimSuffix(name, ".local")
	var start struct {
		Code        string `json:"code"`
		Poll        string `json:"poll"`
		ApprovePath string `json:"approve_path"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := call(config{Server: base}, http.MethodPost, "/api/cli/login", map[string]string{"name": name}, &start); err != nil {
		return err
	}
	link := base + start.ApprovePath
	fmt.Printf("\nApprove this computer in Pier's dashboard:\n\n    %s\n\n", link)
	fmt.Printf("The page shows the code %s. Check it matches, then approve.\n", start.Code)
	openBrowser(link)
	fmt.Print("Waiting…")

	deadline := time.Now().Add(time.Duration(start.ExpiresIn) * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(2 * time.Second)
		var poll struct {
			Status string `json:"status"`
			Token  string `json:"token"`
			Email  string `json:"email"`
		}
		if err := call(config{Server: base}, http.MethodPost, "/api/cli/login/poll", map[string]string{"poll": start.Poll}, &poll); err != nil {
			continue // a blip; keep waiting
		}
		switch poll.Status {
		case "approved":
			if err := saveConfig(config{Server: base, Token: poll.Token, Email: poll.Email}); err != nil {
				return err
			}
			fmt.Printf("\r\x1b[2K%s Signed in to %s as %s.\n", tick(), host(base), poll.Email)
			return nil
		case "denied":
			fmt.Println()
			return errors.New("the dashboard denied this computer")
		case "expired":
			fmt.Println()
			return errors.New("the code ran out before it was approved; run pier login again")
		}
	}
	fmt.Println()
	return errors.New("the code ran out before it was approved; run pier login again")
}

func logout() error {
	c := loadConfig()
	if c.Token == "" {
		fmt.Println("This computer isn't signed in.")
		return nil
	}
	revoked := c.Server != "" && call(c, http.MethodDelete, "/api/session", nil, nil) == nil
	if err := os.Remove(configPath()); err != nil {
		return err
	}
	if revoked {
		fmt.Printf("Signed out of %s.\n", host(c.Server))
	} else {
		fmt.Printf("Signed out of %s locally (couldn't reach the server to revoke the session).\n", host(c.Server))
	}
	return nil
}

func openBrowser(link string) {
	cmd := "xdg-open"
	if runtime.GOOS == "darwin" {
		cmd = "open"
	}
	if _, err := exec.LookPath(cmd); err == nil && term() {
		exec.Command(cmd, link).Start()
	}
}

func host(base string) string {
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	return u.Host
}

// ---- board and deployments ----------------------------------------------------

// colour paints a status the way the board does, on terminals that want it.
func colour(status string) string {
	word := strings.ToUpper(status)
	rgb := map[string]string{
		"live": "244;156;203", "production": "244;156;203", "preview": "188;174;248", "building": "188;174;248",
		"ready": "98;216;198", "failed": "255;180;180",
	}[status]
	return paint(rgb, word)
}

func paint(rgb, s string) string {
	return ink(s, rgb, os.Getenv("NO_COLOR") == "" && term())
}

func tick() string { return paint("98;216;198", "✓") }

func term() bool {
	return terminal.IsTerminal(int(os.Stdout.Fd())) && os.Getenv("TERM") != "dumb"
}

func showBoard(args []string) error {
	fs := flag.NewFlagSet("board", flag.ExitOnError)
	plain := fs.Bool("plain", false, "print a static list instead of the interactive board")
	fs.Parse(args)
	c, err := signedIn()
	if err != nil {
		return err
	}
	if interactive() && !*plain {
		return openCockpit(c, boardView, "", projectsPane, nil)
	}
	var b struct {
		Domain string
		Rows   []struct{ Address, Deployment, Branch, Status, URL string }
	}
	if err := callWith("Loading the board", c, http.MethodGet, "/api/board", nil, &b); err != nil {
		return err
	}
	if term() {
		heading("Deployment board", b.Domain)
	}
	if len(b.Rows) == 0 {
		fmt.Printf("Nothing on %s yet. Deploy a folder to put it on the board:  pier deploy\n", b.Domain)
		return nil
	}
	if term() {
		for _, r := range b.Rows {
			resultRow(r.Address, r.Status, orDash(r.Branch)+", "+orDash(r.Deployment))
		}
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "ADDRESS\tDEPLOYMENT\tBRANCH\tSTATUS")
	for _, r := range b.Rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Address, r.Deployment, r.Branch, colour(r.Status))
	}
	return tw.Flush()
}

func showDeployments(args []string) error {
	fs := flag.NewFlagSet("deployments", flag.ExitOnError)
	plain := fs.Bool("plain", false, "print a static list instead of the interactive browser")
	projectFlag := fs.String("project", "", "the project (default: linked folder or picker)")
	fs.Parse(reorder(args, map[string]bool{"project": true}))
	if fs.NArg() > 1 || (fs.NArg() == 1 && *projectFlag != "") {
		return errors.New("specify one project")
	}
	if fs.NArg() == 1 {
		*projectFlag = fs.Arg(0)
	}
	c, project, err := projectFor(*projectFlag)
	if err != nil {
		return err
	}
	if interactive() && !*plain {
		return openCockpit(c, homeView, project, projectPane, nil)
	}
	var res struct {
		Deployments []struct {
			ID, Branch, Message, Status, Role string
			BuildMS                           int64     `json:"build_ms"`
			CreatedAt                         time.Time `json:"created_at"`
		}
	}
	if err := callWith("Loading "+project+"'s deployments", c, http.MethodGet, "/api/projects/"+url.PathEscape(project)+"/deployments", nil, &res); err != nil {
		return err
	}
	if term() {
		heading("Deployment history", project)
	}
	if len(res.Deployments) == 0 {
		fmt.Printf("%s has no deployments yet. Deploy it:  pier deploy\n", project)
		return nil
	}
	if term() {
		for _, d := range res.Deployments {
			detail := orDash(d.Branch) + ", " + ago(d.CreatedAt)
			if d.BuildMS > 0 {
				detail += fmt.Sprintf(", built in %.1fs", float64(d.BuildMS)/1000)
			}
			resultRow(d.ID+"  "+d.Message, d.Role, detail)
		}
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "DEPLOYMENT\tBRANCH\tSTATUS\tBUILD\tCREATED\tCOMMIT")
	for _, d := range res.Deployments {
		build := "-"
		if d.BuildMS > 0 {
			build = fmt.Sprintf("%.1fs", float64(d.BuildMS)/1000)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", d.ID, orDash(d.Branch), colour(d.Role), build, ago(d.CreatedAt), d.Message)
	}
	return tw.Flush()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
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

// reorder moves flags after positional arguments to the front, so both
// `pier deploy site --prod` and `pier deploy --prod site` work. valued names
// the flags that take a value.
func reorder(args []string, valued map[string]bool) []string {
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") && a != "-" {
			flags = append(flags, a)
			name := strings.TrimLeft(a, "-")
			if valued[name] && !strings.Contains(name, "=") && i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		rest = append(rest, a)
	}
	return append(flags, rest...)
}

// ask reads one line from the terminal.
func ask(prompt string) (string, error) {
	fmt.Print(prompt)
	s, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return "", errors.New("input cancelled")
	}
	return strings.TrimSpace(s), nil
}
