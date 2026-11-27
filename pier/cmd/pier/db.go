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

const dbHelp = `A project's Postgres database. Apps on Pier get DATABASE_URL on their own;
these commands are for you.

  pier db [PROJECT]                    how the database is doing
  pier db create [PROJECT]             add one (--memory 128|256|512|1024|2048|4096, in MB)
  pier db url [PROJECT]                print DATABASE_URL as apps on Pier use it
  pier db tunnel [PROJECT]             reach it from this computer on localhost:5433, and
                                       print the DATABASE_URL for that (--port, --env FILE
                                       to write it into a .env file); Ctrl-C closes it
  pier db psql [PROJECT]               open psql on it, through a tunnel
  pier db backup [PROJECT]             back it up now

--db NAME picks a preview's database instead of production's (see pier db).
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
		Previews    []struct {
			Branch string `json:"branch"`
			Name   string `json:"name"`
			Bytes  int64  `json:"bytes"`
		} `json:"previews"`
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
		case "create", "url", "tunnel", "psql", "backup", "status":
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
	db := fs.String("db", "app", "a preview's database, by name, instead of production's")
	fs.Parse(reorder(args, map[string]bool{"project": true, "memory": true, "port": true, "env": true, "db": true}))
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
			Bytes int64 `json:"bytes"`
		}
		if err := callWith("Backing up "+project+"'s database", c, http.MethodPost, dbPath(project)+"/backups", nil, &b); err != nil {
			return err
		}
		fmt.Printf("%s Backed up %s's database, %s. Download it from the dashboard's Database tab.\n", tick(), project, human(b.Bytes))
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
		{"Disk", human(d.DiskBytes) + ", production's data " + human(d.DataBytes)},
		{"Connections", fmt.Sprintf("%d of %d", d.Connections, d.MaxConns)},
	}
	if len(s.Backups) > 0 {
		b := s.Backups[0]
		lines = append(lines, [2]string{"Last backup", fmt.Sprintf("%s, %s, %s", ago(b.StartedAt), b.Reason, b.State)})
	} else {
		lines = append(lines, [2]string{"Last backup", fmt.Sprintf("none yet; nightly at %02d:00 %s", s.NightlyAt, s.Timezone)})
	}
	for _, p := range d.Previews {
		name := p.Branch
		if name == "" {
			name = "pier deploy previews"
		}
		lines = append(lines, [2]string{"Preview", name + "  " + p.Name})
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
		fmt.Printf("\n  Production started before the database was added, so it has no DATABASE_URL yet.\n  Redeploy it: pier deploy --prod\n")
	}
	fmt.Printf("\n  Apps on Pier get DATABASE_URL on their own. From this computer: pier db tunnel %s\n\n", project)
	return nil
}

func dbCreate(c config, project string, memory int) error {
	if err := callWith("Starting Postgres for "+project, c, http.MethodPost, dbPath(project), map[string]int{"memory_mb": memory}, nil); err != nil {
		return err
	}
	fmt.Printf("%s %s has a Postgres database, with %d MB of memory.\n", tick(), project, memory)
	fmt.Println("  Its apps get DATABASE_URL from their next deploy: pier deploy --prod")
	fmt.Printf("  To reach it from this computer: pier db tunnel %s\n", project)
	return nil
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
