package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const dbHelp = `A project's Postgres. Its server holds any number of databases, each given to
the environments it's linked to; apps on Pier get them on their own, as DATABASE_URL
unless you say otherwise. These commands are for you.

  pier db [PROJECT]                    the server and its databases
  pier db create [PROJECT]             start the server, with "production" for production
                                       and "preview" for previews (--memory 128|256|512|
                                       1024|2048|4096, in MB)
  pier db add NAME [PROJECT]           add a database (--env production,preview or none,
                                       --var the variable, default DATABASE_URL)
  pier db link NAME [PROJECT]          change what it serves (--env, --var)
  pier db rm NAME [PROJECT] --yes      delete a database, after a last backup
  pier db url [PROJECT]                print a database's URL as apps on Pier use it
  pier db external [PROJECT]           make a URL for a service outside Pier, over TLS
  pier db tunnel [PROJECT]             reach a database from this computer on
                                       localhost:5433, and print the URL for that (--port,
                                       --env FILE to write it into a .env file); Ctrl-C
                                       closes it
  pier db psql [PROJECT]               open psql on a database, through a tunnel
  pier db backup [PROJECT]             back up every database now

--db NAME picks a database (default: the one production uses).
PROJECT defaults to the one this folder is linked to.
`

type dbStatus struct {
	Database *struct {
		Image     string    `json:"image"`
		MemoryMB  int       `json:"memory_mb"`
		CreatedAt time.Time `json:"created_at"`
		Version   string    `json:"version"`
		Container struct {
			State       string    `json:"state"`
			Health      string    `json:"health"`
			StartedAt   time.Time `json:"started_at"`
			MemoryBytes int64     `json:"memory_bytes"`
		} `json:"container"`
		DataBytes   int64 `json:"data_bytes"`
		DiskBytes   int64 `json:"disk_bytes"`
		Connections int   `json:"connections"`
		MaxConns    int   `json:"max_connections"`
		Databases   []struct {
			Name         string   `json:"name"`
			Environments []string `json:"environments"`
			Variable     string   `json:"variable"`
			Bytes        int64    `json:"bytes"`
		} `json:"databases"`
		Problem string `json:"problem"`
	} `json:"database"`
	Backups []struct {
		ID        int64     `json:"id"`
		Reason    string    `json:"reason"`
		State     string    `json:"state"`
		Bytes     int64     `json:"bytes"`
		StartedAt time.Time `json:"started_at"`
	} `json:"backups"`
	Production *struct {
		Connected bool `json:"connected"`
	} `json:"production"`
	NightlyAt int    `json:"nightly_at"`
	Timezone  string `json:"timezone"`
}

func dbPath(project string) string { return "/api/projects/" + url.PathEscape(project) + "/database" }

// dbCmd is pier db and its subcommands.
func dbCmd(args []string) error {
	sub := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "create", "add", "link", "rm", "url", "external", "tunnel", "psql", "backup", "status":
			sub, args = args[0], args[1:]
		case "help":
			fmt.Print(dbHelp)
			return nil
		}
	}
	fs := flag.NewFlagSet("db", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, dbHelp) }
	projectFlag := fs.String("project", "", "the project (default: this folder's)")
	memory := fs.Int("memory", 256, "create: the memory limit, in MB")
	port := fs.Int("port", 5433, "tunnel: the port on this computer")
	envFile := fs.String("env", "", "tunnel: write DATABASE_URL into this .env file")
	db := fs.String("db", "", "a database, by name (default: the one production uses)")
	envs := fs.String("envs", "", "add: the environments it serves")
	variable := fs.String("var", "", "add, link: the variable it's given as (DATABASE_URL for a new one)")
	yes := fs.Bool("yes", false, "rm: don't ask")
	flags := map[string]bool{"project": true, "memory": true, "port": true, "env": true, "db": true, "envs": true, "var": true}
	if sub == "add" || sub == "link" {
		// pier db add takes --env for environments; tunnel's --env is a file.
		for i, a := range args {
			if a == "--env" || a == "-env" {
				args[i] = "--envs"
			} else if v, ok := strings.CutPrefix(a, "--env="); ok {
				args[i] = "--envs=" + v
			}
		}
	}
	fs.Parse(reorder(args, flags))
	name := ""
	if sub == "add" || sub == "link" || sub == "rm" {
		if fs.NArg() == 0 {
			return errors.New("name the database: pier db " + sub + " NAME [PROJECT]")
		}
		name = fs.Arg(0)
		rest := fs.Args()[1:]
		fs = flag.NewFlagSet("db", flag.ExitOnError)
		fs.Parse(rest)
	}
	if fs.NArg() > 1 || (fs.NArg() == 1 && *projectFlag != "") {
		return errors.New("say one project: pier db " + strings.TrimSpace(sub+" [PROJECT]"))
	}
	if fs.NArg() == 1 {
		*projectFlag = fs.Arg(0)
	}
	c, project, err := projectFor(*projectFlag)
	if err != nil {
		return err
	}
	switch sub {
	case "create":
		return dbCreate(c, project, *memory)
	case "link":
		var s dbStatus
		if err := call(c, http.MethodGet, dbPath(project), nil, &s); err != nil {
			return err
		}
		body := map[string]any{}
		if s.Database != nil {
			for _, d := range s.Database.Databases {
				if d.Name == name {
					body["environments"], body["variable"] = d.Environments, d.Variable
				}
			}
		}
		if body["variable"] == nil {
			return fmt.Errorf("%s has no database called %s", project, name)
		}
		if *envs != "" {
			list, err := envList(*envs)
			if err != nil {
				return err
			}
			body["environments"] = list
		}
		if *variable != "" {
			body["variable"] = *variable
		}
		if err := callWith("Changing "+name, c, http.MethodPut, dbPath(project)+"/databases/"+url.PathEscape(name), body, nil); err != nil {
			return err
		}
		fmt.Printf("%s Saved. Deploys get the change when they next start.\n", tick())
		return nil
	case "rm":
		if !*yes {
			if !term() {
				return errors.New("deleting a database deletes its data; add --yes")
			}
			answer, err := ask(fmt.Sprintf("Delete %s? A last backup is kept. Type its name: ", name))
			if err != nil || answer != name {
				return errors.New("not deleted")
			}
		}
		if err := callWith("Backing up and deleting "+name, c, http.MethodDelete, dbPath(project)+"/databases/"+url.PathEscape(name), nil, nil); err != nil {
			return err
		}
		fmt.Printf("%s Deleted %s. Its last backup is on the Database tab.\n", tick(), name)
		return nil
	case "add":
		list := []string{}
		for _, e := range strings.Split(*envs, ",") {
			if e = strings.TrimSpace(e); e == "preview" || e == "previews" {
				list = append(list, "preview")
			} else if e == "production" || e == "prod" {
				list = append(list, "production")
			} else if e != "" && e != "none" {
				return fmt.Errorf("%s isn't an environment: say production, preview, or none", e)
			}
		}
		if *variable == "" {
			*variable = "DATABASE_URL"
		}
		body := map[string]any{"name": name, "environments": list, "variable": *variable}
		if err := callWith("Adding "+name, c, http.MethodPost, dbPath(project)+"/databases", body, nil); err != nil {
			return err
		}
		fmt.Printf("%s Added %s to %s's server.\n", tick(), name, project)
		if len(list) > 0 {
			fmt.Printf("  %s gets it as %s from its next deploy.\n", strings.Join(list, " and "), *variable)
		}
		return nil
	case "external":
		target, err := dbName(c, project, *db)
		if err != nil {
			return err
		}
		var r struct {
			URL string `json:"url"`
		}
		if err := callWith("Making an external URL for "+target, c, http.MethodPost, dbPath(project)+"/databases/"+url.PathEscape(target)+"/externals", nil, &r); err != nil {
			return err
		}
		fmt.Println(r.URL)
		if term() {
			fmt.Fprintf(os.Stderr, "\n  It reaches %s from anywhere, over TLS. Revoke it on the dashboard's Database tab.\n", target)
		}
		return nil
	case "url":
		u, err := dbURL(c, project, *db)
		if err != nil {
			return err
		}
		fmt.Println(u)
		return nil
	case "tunnel":
		return dbTunnel(c, project, *db, *port, *envFile)
	case "psql":
		return dbPsql(c, project, *db)
	case "backup":
		var b struct {
			Backups []json.RawMessage `json:"backups"`
			Bytes   int64             `json:"bytes"`
		}
		if err := callWith("Backing up "+project+"'s databases", c, http.MethodPost, dbPath(project)+"/backups", nil, &b); err != nil {
			return err
		}
		fmt.Printf("%s Backed up %s, %s. Download them from the dashboard's Database tab.\n", tick(), plural(len(b.Backups), "database"), human(b.Bytes))
		return nil
	}
	return dbShow(c, project)
}

func dbShow(c config, project string) error {
	var s dbStatus
	if err := callWith("Loading "+project+"'s database", c, http.MethodGet, dbPath(project), nil, &s); err != nil {
		return err
	}
	d := s.Database
	if d == nil {
		fmt.Printf("%s has no database. Add one: pier db create %s\n", project, project)
		return nil
	}
	state := d.Container.State
	if state == "running" && d.Container.Health != "" && d.Container.Health != "healthy" {
		state = d.Container.Health
	}
	memory := fmt.Sprintf("%d MB", d.MemoryMB)
	if d.Container.MemoryBytes > 0 {
		memory = human(d.Container.MemoryBytes) + " of " + memory
	}
	lines := [][2]string{
		{"Postgres", d.Version},
		{"Status", state},
		{"Memory", memory},
		{"Disk", human(d.DiskBytes) + ", data " + human(d.DataBytes)},
		{"Connections", fmt.Sprintf("%d of %d", d.Connections, d.MaxConns)},
	}
	if len(s.Backups) > 0 {
		b := s.Backups[0]
		lines = append(lines, [2]string{"Last backup", fmt.Sprintf("%s, %s, %s", ago(b.StartedAt), b.Reason, b.State)})
	} else {
		lines = append(lines, [2]string{"Last backup", fmt.Sprintf("none yet; nightly at %02d:00 %s", s.NightlyAt, s.Timezone)})
	}
	for _, p := range d.Databases {
		serves := "nothing"
		if len(p.Environments) > 0 {
			serves = strings.Join(p.Environments, " and ") + " as " + p.Variable
		}
		lines = append(lines, [2]string{"Database", fmt.Sprintf("%s  %s, %s", p.Name, serves, human(p.Bytes))})
	}
	if term() {
		heading("Database", project)
	}
	for _, l := range lines {
		fmt.Printf("  %-12s %s\n", l[0], l[1])
	}
	if d.Problem != "" {
		fmt.Printf("\n  %s\n", d.Problem)
	}
	if s.Production != nil && !s.Production.Connected {
		fmt.Printf("\n  Production started before the database was added, so it doesn't have it yet.\n  Redeploy it: pier deploy --prod\n")
	}
	fmt.Printf("\n  Apps on Pier get their databases on their own. From this computer: pier db tunnel %s\n\n", project)
	return nil
}

func dbCreate(c config, project string, memory int) error {
	if err := callWith("Starting Postgres for "+project, c, http.MethodPost, dbPath(project), map[string]int{"memory_mb": memory}, nil); err != nil {
		return err
	}
	fmt.Printf("%s %s has Postgres, with %d MB of memory: production and preview get a database each.\n", tick(), project, memory)
	fmt.Println("  Its apps get DATABASE_URL from their next deploy: pier deploy --prod")
	fmt.Printf("  To reach it from this computer: pier db tunnel %s\n", project)
	return nil
}

// dbName is the database a command acts on: the one named, or the one
// production uses.
func dbName(c config, project, db string) (string, error) {
	if db != "" {
		return db, nil
	}
	var s dbStatus
	if err := call(c, http.MethodGet, dbPath(project), nil, &s); err != nil {
		return "", err
	}
	if s.Database == nil || len(s.Database.Databases) == 0 {
		return "", fmt.Errorf("%s has no database. Add one: pier db create %s", project, project)
	}
	for _, d := range s.Database.Databases {
		for _, e := range d.Environments {
			if e == "production" {
				return d.Name, nil
			}
		}
	}
	return s.Database.Databases[0].Name, nil
}

func dbURL(c config, project, db string) (string, error) {
	var r struct {
		URL string `json:"url"`
	}
	if err := call(c, http.MethodGet, dbPath(project)+"/url?db="+url.QueryEscape(db), nil, &r); err != nil {
		return "", err
	}
	return r.URL, nil
}

// localURL is a database's address through a tunnel on this computer.
func localURL(u string, port int) (string, error) {
	parsed, err := url.Parse(u)
	if err != nil {
		return "", err
	}
	parsed.Host = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	return parsed.String(), nil
}

// tunnel listens on a port on this computer and joins each connection to
// the project's Postgres server, through pierd, until ctx ends.
type tunnel struct {
	c       config
	project string
	ln      net.Listener
	wg      sync.WaitGroup
}

func openTunnel(c config, project string, port int) (*tunnel, error) {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("port %d on this computer is taken; choose another with --port", port)
	}
	return &tunnel{c: c, project: project, ln: ln}, nil
}

func (t *tunnel) port() int { return t.ln.Addr().(*net.TCPAddr).Port }

// check makes one connection through pierd, so a tunnel that can't work
// says so before anything uses it.
func (t *tunnel) check(ctx context.Context) error {
	conn, err := t.dial(ctx)
	if err != nil {
		return err
	}
	conn.Close()
	return nil
}

func (t *tunnel) dial(ctx context.Context) (io.ReadWriteCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.c.Server+dbPath(t.project)+"/tunnel", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+t.c.Token)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "pier-postgres")
	resp, err := (&http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext}}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("can't reach Pier at %s: %w", t.c.Server, err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		defer resp.Body.Close()
		var e struct{ Error string }
		if decodeJSON(resp.Body, &e) == nil && e.Error != "" {
			return nil, errors.New(e.Error)
		}
		return nil, fmt.Errorf("Pier answered %s", resp.Status)
	}
	rwc, ok := resp.Body.(io.ReadWriteCloser)
	if !ok {
		resp.Body.Close()
		return nil, errors.New("the connection to Pier can't carry a tunnel")
	}
	return rwc, nil
}

func (t *tunnel) serve(ctx context.Context) {
	go func() {
		<-ctx.Done()
		t.ln.Close()
	}()
	for {
		local, err := t.ln.Accept()
		if err != nil {
			return
		}
		t.wg.Add(1)
		go func() {
			defer t.wg.Done()
			defer local.Close()
			remote, err := t.dial(ctx)
			if err != nil {
				fmt.Fprintln(os.Stderr, "pier: a connection through the tunnel failed:", err)
				return
			}
			defer remote.Close()
			done := make(chan struct{}, 2)
			go func() { io.Copy(remote, local); done <- struct{}{} }()
			go func() { io.Copy(local, remote); done <- struct{}{} }()
			select {
			case <-done:
			case <-ctx.Done():
			}
		}()
	}
}

func dbTunnel(c config, project, db string, port int, envFile string) error {
	u, err := dbURL(c, project, db)
	if err != nil {
		return err
	}
	t, err := openTunnel(c, project, port)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := t.check(ctx); err != nil {
		t.ln.Close()
		return err
	}
	local, err := localURL(u, t.port())
	if err != nil {
		return err
	}
	if envFile != "" {
		if err := writeEnv(envFile, "DATABASE_URL", local); err != nil {
			return err
		}
	}
	if term() {
		heading("Database tunnel", project)
		fmt.Printf("  %s localhost:%d reaches %s's %s database.\n\n", tick(), t.port(), project, db)
		fmt.Printf("  DATABASE_URL=%s\n\n", local)
		if envFile != "" {
			fmt.Printf("  Written to %s.\n", envFile)
		}
		fmt.Println("  Ctrl-C closes the tunnel.")
	} else {
		fmt.Println("DATABASE_URL=" + local)
	}
	t.serve(ctx)
	t.wg.Wait()
	return nil
}

func dbPsql(c config, project, db string) error {
	psql, err := exec.LookPath("psql")
	if err != nil {
		return errors.New("psql isn't installed here; open a tunnel with pier db tunnel and use any Postgres client")
	}
	u, err := dbURL(c, project, db)
	if err != nil {
		return err
	}
	t, err := openTunnel(c, project, 0)
	if err != nil {
		return err
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	if err := t.check(ctx); err != nil {
		t.ln.Close()
		return err
	}
	go t.serve(ctx)
	local, err := localURL(u, t.port())
	if err != nil {
		return err
	}
	cmd := exec.Command(psql, local)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	// Ctrl-C belongs to psql, which cancels a query with it.
	signal.Ignore(os.Interrupt)
	err = cmd.Run()
	stop()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		os.Exit(exit.ExitCode())
	}
	return err
}

// writeEnv sets one variable in a .env file, replacing its line if it has
// one. A file it makes is readable by its owner alone.
func writeEnv(path, name, value string) error {
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	line := name + "=" + value
	var out []string
	found := false
	for _, l := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "export ")), name+"=") {
			if !found {
				out = append(out, line)
				found = true
			}
			continue
		}
		if l != "" || len(out) > 0 {
			out = append(out, l)
		}
	}
	if !found {
		out = append(out, line)
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(strings.Join(out, "\n")+"\n"), 0o600)
}

func decodeJSON(r io.Reader, into any) error {
	return json.NewDecoder(io.LimitReader(r, 1<<20)).Decode(into)
}
