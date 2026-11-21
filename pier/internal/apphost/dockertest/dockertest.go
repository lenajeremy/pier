// Package dockertest stands in for the Docker engine in tests of code that
// drives apphost.Host. It keeps containers in memory and answers only the
// API calls Pier makes to stop, start and find them. Each container has a
// real loopback listener, so readiness probes and proxy addresses work.
package dockertest

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
)

// Container is one application container as a test sets it up.
type Container struct {
	Name    string
	Labels  map[string]string // apphost.Host.Labels() for one this Pier owns
	Running bool
	Policy  string // the restart policy it was created with
	Crashes bool   // its process exits as soon as it's started
}

type app struct {
	Container
	port   int
	server *httptest.Server
}

// Engine is the stand-in engine. Its exported fields are set before use.
type Engine struct {
	// Probed is told each time a running container's HTTP listener is asked
	// something, which is how Pier checks that an application is ready.
	Probed func(name string)

	mu    sync.Mutex
	apps  map[string]*app
	down  bool
	calls []string
}

// Start serves an empty engine on a unix socket and points DOCKER_HOST at it
// for the rest of the test.
func Start(t testing.TB) *Engine {
	t.Helper()
	// A unix socket's path is short, so this isn't under t.TempDir().
	dir, err := os.MkdirTemp("", "pier-docker")
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatal(err)
	}
	e := &Engine{apps: map[string]*app{}}
	srv := &http.Server{Handler: e}
	go srv.Serve(ln)
	t.Cleanup(func() {
		srv.Close()
		os.RemoveAll(dir)
		e.mu.Lock()
		defer e.mu.Unlock()
		for _, a := range e.apps {
			a.server.Close()
		}
	})
	t.Setenv("DOCKER_HOST", "unix://"+sock)
	t.Setenv("DOCKER_CONTEXT", "")
	return e
}

// Add creates a container and returns the loopback address it publishes.
func (e *Engine) Add(c Container) string {
	a := &app{Container: c}
	a.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		e.mu.Lock()
		running, probed := a.Running, e.Probed
		e.mu.Unlock()
		if !running {
			http.Error(w, "stopped", http.StatusServiceUnavailable)
			return
		}
		if probed != nil {
			probed(a.Name)
		}
		w.Write([]byte("hello from " + a.Name))
	}))
	a.port = a.server.Listener.Addr().(*net.TCPAddr).Port
	e.mu.Lock()
	defer e.mu.Unlock()
	e.apps[c.Name] = a
	return a.server.Listener.Addr().String()
}

// SetDown makes every call fail, as while the engine restarts or is too busy
// to answer.
func (e *Engine) SetDown(down bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.down = down
}

// Running names the containers that are up, sorted.
func (e *Engine) Running() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var names []string
	for name, a := range e.apps {
		if a.Running {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// Policy is a container's restart policy now.
func (e *Engine) Policy(name string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.apps[name].Policy
}

// Calls lists what changed containers so far, like "stop pier-app-1".
func (e *Engine) Calls() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.calls)
}

var (
	version  = regexp.MustCompile(`^/v[0-9.]+`)
	loopback = netip.MustParseAddr("127.0.0.1")
	appPort  = network.MustParsePort("8080/tcp")
)

func (e *Engine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := version.ReplaceAllString(r.URL.Path, "")
	if path == "/_ping" {
		w.Write([]byte("OK"))
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.down {
		fail(w, http.StatusInternalServerError, "the engine is restarting")
		return
	}
	if r.Method == http.MethodGet && path == "/containers/json" {
		e.list(w, r)
		return
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 3 || parts[0] != "containers" {
		fail(w, http.StatusNotImplemented, "dockertest doesn't answer "+r.Method+" "+path)
		return
	}
	a := e.find(parts[1])
	if a == nil {
		fail(w, http.StatusNotFound, "No such container: "+parts[1])
		return
	}
	switch r.Method + " " + parts[2] {
	case "GET json":
		reply(w, a.inspect())
	case "POST start":
		e.change(w, a, "start", !a.Running, !a.Crashes)
	case "POST stop":
		e.change(w, a, "stop", a.Running, false)
	case "POST restart":
		e.change(w, a, "restart", true, !a.Crashes)
	case "POST update":
		var cfg container.UpdateConfig
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		a.Policy = string(cfg.RestartPolicy.Name)
		e.calls = append(e.calls, "update "+a.Name+" "+a.Policy)
		reply(w, container.UpdateResponse{})
	default:
		fail(w, http.StatusNotImplemented, "dockertest doesn't answer "+r.Method+" "+path)
	}
}

// change starts or stops a container, or says nothing changed.
func (e *Engine) change(w http.ResponseWriter, a *app, verb string, needed, running bool) {
	if !needed {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	a.Running = running
	e.calls = append(e.calls, verb+" "+a.Name)
	w.WriteHeader(http.StatusNoContent)
}

// find looks a container up by name or by ID, as the engine does.
func (e *Engine) find(ref string) *app {
	return e.apps[strings.TrimPrefix(ref, "id-")]
}

func (a *app) inspect() container.InspectResponse {
	state := &container.State{Status: container.StateExited}
	settings := &container.NetworkSettings{}
	if a.Crashes {
		state.ExitCode = 23
	}
	if a.Running {
		state = &container.State{Status: container.StateRunning, Running: true}
		settings.Ports = network.PortMap{appPort: {{HostIP: loopback, HostPort: strconv.Itoa(a.port)}}}
	}
	return container.InspectResponse{
		ID: "id-" + a.Name, Name: "/" + a.Name, State: state, NetworkSettings: settings,
		Config:     &container.Config{Labels: a.Labels},
		HostConfig: &container.HostConfig{RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyMode(a.Policy)}},
	}
}

// list answers with running containers, or all of them, that carry every
// label the caller filters by.
func (e *Engine) list(w http.ResponseWriter, r *http.Request) {
	var filters map[string]map[string]bool
	if raw := r.URL.Query().Get("filters"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &filters); err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	all, _ := strconv.ParseBool(r.URL.Query().Get("all"))
	out := []container.Summary{}
	for _, a := range e.apps {
		if (!a.Running && !all) || !a.labelled(filters["label"]) {
			continue
		}
		s := container.Summary{ID: "id-" + a.Name, Names: []string{"/" + a.Name}, Labels: a.Labels, State: container.StateExited}
		if a.Running {
			s.State = container.StateRunning
			s.Ports = []container.PortSummary{{IP: loopback, PrivatePort: appPort.Num(), PublicPort: uint16(a.port), Type: "tcp"}}
		}
		out = append(out, s)
	}
	reply(w, out)
}

func (a *app) labelled(want map[string]bool) bool {
	for label := range want {
		key, value, _ := strings.Cut(label, "=")
		if a.Labels[key] != value {
			return false
		}
	}
	return true
}

func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"message": message})
}
