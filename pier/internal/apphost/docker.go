// Package apphost runs immutable HTTP deployments on the local Docker engine.
package apphost

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

const ownerLabel = "io.pier.instance"

var loopback = netip.MustParseAddr("127.0.0.1")

type Host struct {
	owner  string
	mu     sync.Mutex
	client *client.Client
}

func New(data string) *Host {
	hash := sha256.Sum256([]byte(data))
	return &Host{owner: fmt.Sprintf("pier-%x", hash[:8])}
}

// Labels mark a container or network as this instance's. Everything Pier
// creates carries them, and it touches nothing without them.
func (h *Host) Labels() map[string]string { return map[string]string{ownerLabel: h.owner} }

// Connect lazily so a static site or the dashboard can run with Docker offline.
func (h *Host) engine(ctx context.Context) (*client.Client, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.client != nil {
		return h.client, nil
	}
	host := os.Getenv("DOCKER_HOST")
	if host == "" || os.Getenv("DOCKER_CONTEXT") != "" {
		raw, err := exec.CommandContext(ctx, "docker", "context", "inspect", "--format", "{{.Endpoints.docker.Host}}").Output()
		if err != nil {
			return nil, fmt.Errorf("find local Docker engine: %w", err)
		}
		host = strings.TrimSpace(string(raw))
	}
	if !strings.HasPrefix(host, "unix://") {
		return nil, errors.New("Pier web processes require a local Docker Unix socket")
	}
	cli, err := client.New(client.WithHost(host))
	if err == nil {
		h.client = cli
	}
	return cli, err
}

func (h *Host) ensureNetwork(ctx context.Context, c *client.Client) error {
	netw, err := c.NetworkInspect(ctx, h.owner, client.NetworkInspectOptions{})
	if errdefs.IsNotFound(err) {
		_, err = c.NetworkCreate(ctx, h.owner, client.NetworkCreateOptions{Driver: "bridge", Labels: h.Labels(), Options: map[string]string{"com.docker.network.bridge.enable_icc": "false"}})
		if err != nil && !errdefs.IsConflict(err) {
			return err
		}
		netw, err = c.NetworkInspect(ctx, h.owner, client.NetworkInspectOptions{})
	}
	if err != nil {
		return err
	}
	if netw.Network.Labels[ownerLabel] != h.owner || netw.Network.Options["com.docker.network.bridge.enable_icc"] != "false" {
		return errors.New("the Pier application network has incompatible settings")
	}
	return nil
}

func envValue(env []string, key string) string {
	for _, v := range env {
		if value, ok := strings.CutPrefix(v, key+"="); ok {
			return value
		}
	}
	return ""
}

func appPort(env []string, exposed map[string]struct{}) (network.Port, error) {
	raw := envValue(env, "PORT")
	if raw == "" {
		for k := range exposed {
			if strings.HasSuffix(k, "/tcp") {
				if raw != "" {
					return network.Port{}, errors.New("image exposes multiple TCP ports; set the project's PORT variable")
				}
				raw = strings.TrimSuffix(k, "/tcp")
			}
		}
	}
	if raw == "" {
		raw = "8080"
	}
	n, err := strconv.ParseUint(raw, 10, 16)
	if err != nil || n == 0 {
		return network.Port{}, errors.New("PORT must be a number from 1 to 65535")
	}
	port, _ := network.PortFrom(uint16(n), network.TCP)
	return port, nil
}

// DefaultMemoryMB caps server containers at 1 GB when a project leaves memory
// unset, so one runaway app cannot OOM-kill the rest of the server.
const DefaultMemoryMB = 1024

// StartOptions configures resource limits and startup overrides for a container.
// MemoryMB 0 applies DefaultMemoryMB; negative disables the memory limit.
type StartOptions struct {
	StartCommand string
	HealthPath   string
	MemoryMB     int
	CPUs         float64
}

// ContainerStatus summarizes the live state of an application container.
type ContainerStatus struct {
	State         string    `json:"state"`
	Health        string    `json:"health,omitempty"`
	ExitCode      int       `json:"exit_code,omitempty"`
	OOMKilled     bool      `json:"oom_killed,omitempty"`
	Restarts      int       `json:"restarts,omitempty"`
	StartedAt     time.Time `json:"started_at,omitempty"`
	MemoryLimitMB int64     `json:"memory_limit_mb,omitempty"`
	CPULimit      float64   `json:"cpu_limit,omitempty"`
}

func resolveResources(opts StartOptions) container.Resources {
	limit := int64(512)
	res := container.Resources{PidsLimit: &limit}
	memMB := opts.MemoryMB
	if memMB == 0 {
		memMB = DefaultMemoryMB
	}
	if memMB > 0 {
		res.Memory = int64(memMB) * 1024 * 1024
	}
	if opts.CPUs > 0 {
		res.NanoCPUs = int64(opts.CPUs * 1e9)
	}
	return res
}

func withHealthPath(env []string, healthPath string) []string {
	if healthPath == "" {
		return env
	}
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if strings.HasPrefix(kv, "PIER_HEALTH_PATH=") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "PIER_HEALTH_PATH="+healthPath)
}

func (h *Host) Start(ctx context.Context, name string, env []string) error {
	return h.StartWithOptions(ctx, name, env, StartOptions{})
}

func (h *Host) StartWithOptions(ctx context.Context, name string, env []string, opts StartOptions) error {
	c, err := h.engine(ctx)
	if err != nil {
		return err
	}
	img, err := c.ImageInspect(ctx, name+":app")
	if err != nil {
		return err
	}
	if img.Config == nil {
		return errors.New("image has no container configuration")
	}
	env = withHealthPath(env, opts.HealthPath)
	port, err := appPort(append(append([]string{}, env...), img.Config.Env...), img.Config.ExposedPorts)
	if err != nil {
		return err
	}
	if envValue(env, "PORT") == "" {
		env = append(env, "PORT="+port.Port())
	}
	if err := h.ensureNetwork(ctx, c); err != nil {
		return err
	}
	// Pin the loopback mapping in Docker so another app cannot inherit this
	// app's old proxy address when Docker restarts containers in a new order.
	// Docker rechecks the bind on start, before the deployment becomes ready.
	socket, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	published := strconv.Itoa(socket.Addr().(*net.TCPAddr).Port)
	if err := socket.Close(); err != nil {
		return err
	}
	config := &container.Config{Env: env, Labels: h.Labels(), ExposedPorts: network.PortSet{port: {}}}
	if opts.StartCommand != "" {
		config.Entrypoint = []string{"/bin/sh", "-c"}
		config.Cmd = []string{opts.StartCommand}
	}
	host := &container.HostConfig{NetworkMode: container.NetworkMode(h.owner), PortBindings: network.PortMap{port: {{HostIP: loopback, HostPort: published}}},
		RestartPolicy: restartPolicy, SecurityOpt: []string{"no-new-privileges"}, Resources: resolveResources(opts),
		LogConfig: container.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "10m", "max-file": "2"}}}
	created, err := c.ContainerCreate(ctx, client.ContainerCreateOptions{Name: name, Image: img.ID, Config: config, HostConfig: host})
	if err != nil {
		return err
	}
	if _, err = c.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		return err
	}
	return h.Ready(ctx, name)
}

// Status inspects an owned application container and reports its health, restart count, and limits.
func (h *Host) Status(ctx context.Context, name string) (ContainerStatus, error) {
	c, err := h.engine(ctx)
	if err != nil {
		return ContainerStatus{}, err
	}
	res, err := c.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	if err != nil {
		if errdefs.IsNotFound(err) {
			return ContainerStatus{State: "not_found"}, nil
		}
		return ContainerStatus{}, err
	}
	app := res.Container
	if app.Config == nil || app.Config.Labels[ownerLabel] != h.owner {
		return ContainerStatus{}, errors.New("application container ownership mismatch")
	}
	st := ContainerStatus{State: "unknown", Restarts: app.RestartCount}
	if app.State != nil {
		st.State = string(app.State.Status)
		st.ExitCode = app.State.ExitCode
		st.OOMKilled = app.State.OOMKilled
		if app.State.OOMKilled && !app.State.Running {
			st.State = "oom_killed"
		}
		if app.State.Health != nil {
			st.Health = string(app.State.Health.Status)
		}
		if t, err := time.Parse(time.RFC3339Nano, app.State.StartedAt); err == nil && !t.IsZero() && t.Year() > 1 {
			st.StartedAt = t
		}
	}
	if app.HostConfig != nil {
		if app.HostConfig.Memory > 0 {
			st.MemoryLimitMB = app.HostConfig.Memory / (1024 * 1024)
		}
		if app.HostConfig.NanoCPUs > 0 {
			st.CPULimit = float64(app.HostConfig.NanoCPUs) / 1e9
		}
	}
	return st, nil
}

// Restart restarts an owned application container and waits for its HTTP readiness check.
func (h *Host) Restart(ctx context.Context, name string) error {
	c, err := h.engine(ctx)
	if err != nil {
		return err
	}
	res, err := c.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	if err != nil {
		if errdefs.IsNotFound(err) {
			return ErrNotFound
		}
		return err
	}
	if res.Container.Config == nil || res.Container.Config.Labels[ownerLabel] != h.owner {
		return errors.New("application container ownership mismatch")
	}
	timeout := 10
	if _, err := c.ContainerRestart(ctx, res.Container.ID, client.ContainerRestartOptions{Timeout: &timeout}); err != nil {
		return err
	}
	return h.Ready(ctx, name)
}

// restartPolicy brings an application back after a crash or a reboot, but
// leaves one that Pier stopped alone. "always" would start every superseded
// deployment again whenever Docker restarts.
var restartPolicy = container.RestartPolicy{Name: container.RestartPolicyUnlessStopped}

// owned inspects one of this instance's containers by name.
func (h *Host) owned(ctx context.Context, c *client.Client, name string) (container.InspectResponse, error) {
	res, err := c.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	if errdefs.IsNotFound(err) {
		return container.InspectResponse{}, ErrNotFound
	}
	if err != nil {
		return container.InspectResponse{}, err
	}
	if res.Container.Config == nil || res.Container.Config.Labels[ownerLabel] != h.owner {
		return container.InspectResponse{}, errors.New("application container ownership mismatch")
	}
	return res.Container, nil
}

// Stop stops a superseded deployment's container. The container and its image
// stay, so Resume can bring the deployment back. One that is already gone or
// stopped is left as it is.
func (h *Host) Stop(ctx context.Context, name string) error {
	c, err := h.engine(ctx)
	if err != nil {
		return err
	}
	app, err := h.owned(ctx, c, name)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	// Containers made before restartPolicy changed would come back at the
	// next reboot.
	if app.HostConfig != nil && app.HostConfig.RestartPolicy.Name != restartPolicy.Name {
		if _, err := c.ContainerUpdate(ctx, app.ID, client.ContainerUpdateOptions{RestartPolicy: &restartPolicy}); err != nil {
			return err
		}
	}
	if app.State != nil && !app.State.Running && !app.State.Restarting {
		return nil
	}
	_, err = c.ContainerStop(ctx, app.ID, client.ContainerStopOptions{})
	return err
}

// Resume makes sure a deployment's container is running and answering, and
// starts it first if it was stopped. One that was started and then doesn't
// answer is stopped again, or Docker would keep restarting a process that
// crashes at boot.
func (h *Host) Resume(ctx context.Context, name string) error {
	c, err := h.engine(ctx)
	if err != nil {
		return err
	}
	app, err := h.owned(ctx, c, name)
	if err != nil {
		return err
	}
	if app.State != nil && app.State.Running {
		return h.Ready(ctx, name)
	}
	if _, err := c.ContainerStart(ctx, app.ID, client.ContainerStartOptions{}); err != nil {
		return err
	}
	err = h.Ready(ctx, name)
	if err != nil {
		// The caller may have given up, and the stop must still happen.
		stop, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		c.ContainerStop(stop, app.ID, client.ContainerStopOptions{})
	}
	return err
}

// Running names this instance's containers that are up, whether or not they
// answer yet.
func (h *Host) Running(ctx context.Context) (map[string]bool, error) {
	c, err := h.engine(ctx)
	if err != nil {
		return nil, err
	}
	list, err := c.ContainerList(ctx, client.ContainerListOptions{Filters: client.Filters{}.Add("label", ownerLabel+"="+h.owner)})
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, app := range list.Items {
		for _, name := range app.Names {
			out[strings.TrimPrefix(name, "/")] = true
		}
	}
	return out, nil
}

var probeClient = &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func (h *Host) Ready(ctx context.Context, name string) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	c, err := h.engine(ctx)
	if err != nil {
		return err
	}
	ticker := time.NewTicker(350 * time.Millisecond)
	defer ticker.Stop()
	reason := "waiting for the HTTP listener"
	for {
		result, err := c.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
		if err != nil {
			return err
		}
		app := result.Container
		if app.Config == nil || app.Config.Labels[ownerLabel] != h.owner {
			return errors.New("application container ownership mismatch")
		}
		if app.State == nil {
			return errors.New("Docker returned no application state")
		}
		if !app.State.Running || app.State.Restarting || app.State.Paused {
			return fmt.Errorf("application process is %s (exit code %d)", app.State.Status, app.State.ExitCode)
		}
		if app.State.Health != nil && app.State.Health.Status == container.Unhealthy {
			return errors.New("image HEALTHCHECK failed")
		}
		if app.NetworkSettings != nil && (app.State.Health == nil || app.State.Health.Status != container.Starting) {
			path := envValue(app.Config.Env, "PIER_HEALTH_PATH")
			strict := path != ""
			if !strict {
				path = "/"
			}
			u, err := url.ParseRequestURI(path)
			if err != nil || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
				return errors.New("PIER_HEALTH_PATH must be a local HTTP path")
			}
			for _, bindings := range app.NetworkSettings.Ports {
				for _, b := range bindings {
					if b.HostIP != loopback || b.HostPort == "" {
						continue
					}
					u.Scheme = "http"
					u.Host = "127.0.0.1:" + b.HostPort
					req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
					if err != nil {
						return err
					}
					resp, err := probeClient.Do(req)
					if err == nil {
						resp.Body.Close()
						if resp.StatusCode < 400 || (!strict && resp.StatusCode < 500) {
							return nil
						}
						reason = "HTTP " + resp.Status
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("application readiness: %s: %w", reason, ctx.Err())
		case <-ticker.C:
		}
	}
}

// Endpoints reads the active bindings after a Docker or Pier restart. Only
// this Pier instance's running containers and loopback listeners qualify.
func (h *Host) Endpoints(ctx context.Context) (map[string]string, error) {
	c, err := h.engine(ctx)
	if err != nil {
		return nil, err
	}
	list, err := c.ContainerList(ctx, client.ContainerListOptions{Filters: client.Filters{}.Add("label", ownerLabel+"="+h.owner)})
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, app := range list.Items {
		if app.State != "running" || (app.Health != nil && (app.Health.Status == container.Unhealthy || app.Health.Status == container.Starting)) {
			continue
		}
		for _, p := range app.Ports {
			if p.IP == loopback && p.PublicPort > 0 && p.Type == "tcp" {
				for _, name := range app.Names {
					out[strings.TrimPrefix(name, "/")] = fmt.Sprintf("127.0.0.1:%d", p.PublicPort)
				}
			}
		}
	}
	return out, nil
}

func (h *Host) Remove(ctx context.Context, name string) error {
	c, err := h.engine(ctx)
	if err != nil {
		return err
	}
	res, err := c.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	if err == nil {
		if res.Container.Config.Labels[ownerLabel] != h.owner {
			return errors.New("refusing to remove a container owned by another instance")
		}
		_, err = c.ContainerRemove(ctx, res.Container.ID, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
	}
	if err != nil && !errdefs.IsNotFound(err) {
		return err
	}
	_, err = c.ImageRemove(ctx, name+":app", client.ImageRemoveOptions{})
	if errdefs.IsNotFound(err) {
		return nil
	}
	return err
}

// ErrNotFound means the deployment's container does not exist on the Docker host.
var ErrNotFound = errors.New("application container not found")

// LogLine is one stdout or stderr line captured from a running application container.
type LogLine struct {
	At     string `json:"at,omitempty"`
	Stream string `json:"stream"`
	Text   string `json:"text"`
}

const maxLogBytes = 512 << 10

// Logs reads recent stdout and stderr lines from an owned application container.
func (h *Host) Logs(ctx context.Context, name string, tail int, since string) ([]LogLine, error) {
	rc, tty, err := h.OpenLogs(ctx, name, tail, since, false)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	raw, err := io.ReadAll(io.LimitReader(rc, maxLogBytes))
	if err != nil {
		return nil, err
	}
	return parseContainerLogs(raw, tty), nil
}

// parseContainerLogs decodes Docker's multiplexed log stream (or raw TTY stream)
// into structured lines with optional RFC3339 timestamps stripped into At.
func parseContainerLogs(raw []byte, tty bool) []LogLine {
	var lines []LogLine
	appendChunk := func(stream string, chunk []byte) {
		for _, line := range bytes.Split(chunk, []byte("\n")) {
			line = bytes.TrimSuffix(line, []byte("\r"))
			if len(line) == 0 {
				continue
			}
			lines = append(lines, containerLine(stream, line))
		}
	}
	if tty {
		appendChunk("stdout", raw)
		return lines
	}
	for len(raw) >= 8 {
		streamType := raw[0]
		if (streamType != 1 && streamType != 2) || raw[1] != 0 || raw[2] != 0 || raw[3] != 0 {
			appendChunk("stdout", raw)
			break
		}
		n := int(binary.BigEndian.Uint32(raw[4:8]))
		raw = raw[8:]
		if n > len(raw) {
			n = len(raw)
		}
		stream := "stdout"
		if streamType == 2 {
			stream = "stderr"
		}
		appendChunk(stream, raw[:n])
		raw = raw[n:]
	}
	return lines
}

// OpenLogs opens an owned container's Docker stream, optionally following it.
func (h *Host) OpenLogs(ctx context.Context, name string, tail int, since string, follow bool) (io.ReadCloser, bool, error) {
	if tail <= 0 {
		tail = 200
	}
	if tail > 1000 {
		tail = 1000
	}
	c, err := h.engine(ctx)
	if err != nil {
		return nil, false, err
	}
	res, err := c.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	if err != nil {
		if errdefs.IsNotFound(err) {
			return nil, false, ErrNotFound
		}
		return nil, false, err
	}
	if res.Container.Config == nil || res.Container.Config.Labels[ownerLabel] != h.owner {
		return nil, false, errors.New("application container ownership mismatch")
	}
	rc, err := c.ContainerLogs(ctx, res.Container.ID, client.ContainerLogsOptions{
		ShowStdout: true, ShowStderr: true, Timestamps: true, Follow: follow, Tail: strconv.Itoa(tail), Since: since,
	})
	if err != nil {
		return nil, false, err
	}
	return rc, res.Container.Config.Tty, nil
}

// FollowLogLines preserves partial lines across Docker frames and keeps the
// two output streams separate, even when their frames are interleaved.
func FollowLogLines(r io.Reader, tty bool, emit func(LogLine) error) error {
	var pending [2][]byte
	chunk := func(index int, p []byte, final bool) error {
		for len(p) > 0 || (final && len(pending[index]) > 0) {
			part, rest, complete := bytes.Cut(p, []byte{'\n'})
			if len(pending[index])+len(part) > maxLogBytes {
				return errors.New("container log line exceeds 512 KiB")
			}
			pending[index] = append(pending[index], part...)
			if complete || final {
				stream := [2]string{"stdout", "stderr"}[index]
				line := containerLine(stream, pending[index])
				if err := emit(line); err != nil {
					return err
				}
				pending[index] = pending[index][:0]
			}
			p = rest
		}
		return nil
	}
	if tty {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 4096), maxLogBytes)
		for sc.Scan() {
			if err := emit(containerLine("stdout", sc.Bytes())); err != nil {
				return err
			}
		}
		return sc.Err()
	}
	var header [8]byte
	var buf [16384]byte
	for {
		_, err := io.ReadFull(r, header[:])
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		index := int(header[0]) - 1
		if index < 0 || index > 1 {
			return fmt.Errorf("unexpected Docker log stream %d", header[0])
		}
		remaining := int64(binary.BigEndian.Uint32(header[4:]))
		for remaining > 0 {
			n, err := io.ReadFull(r, buf[:min(remaining, int64(len(buf)))])
			if err != nil {
				return err
			}
			if err := chunk(index, buf[:n], false); err != nil {
				return err
			}
			remaining -= int64(n)
		}
	}
	for index := range pending {
		if err := chunk(index, nil, true); err != nil {
			return err
		}
	}
	return nil
}

func containerLine(stream string, raw []byte) LogLine {
	line := LogLine{Stream: stream, Text: strings.TrimSuffix(string(raw), "\r")}
	if ts, rest, ok := strings.Cut(line.Text, " "); ok {
		if _, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			line.At, line.Text = ts, rest
		}
	}
	return line
}
