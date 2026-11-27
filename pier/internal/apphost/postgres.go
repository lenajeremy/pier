package apphost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

// databaseLabel marks a project's Postgres container, its volume and its
// network with the project's name.
const databaseLabel = "io.pier.database"

// DatabaseHost is the name a project's apps reach its Postgres server by,
// on the project's own network.
const DatabaseHost = "postgres"

// PostgresSpec is one project's Postgres server.
type PostgresSpec struct {
	Project  string
	Image    string   // postgres:18-alpine
	MemoryMB int      // the container's limit; the server's settings are sized from it
	Password string   // the postgres superuser's, used only the first time the volume starts
	Settings []string // name=value, passed to postgres as -c flags
}

// DatabaseState is how a project's Postgres container is doing.
type DatabaseState struct {
	State       string    `json:"state"`            // running, restarting, exited, or missing
	Health      string    `json:"health,omitempty"` // healthy, unhealthy or starting
	StartedAt   time.Time `json:"started_at,omitzero"`
	Restarts    int       `json:"restarts,omitempty"`
	OOMKilled   bool      `json:"oom_killed,omitempty"`
	MemoryBytes uint64    `json:"memory_bytes,omitempty"` // in use now, page cache left out
	LimitBytes  uint64    `json:"limit_bytes,omitempty"`
}

// DatabaseContainer is the name of a project's Postgres container. Its
// volume has the same name.
func (h *Host) DatabaseContainer(project string) string { return h.owner + "-db-" + project }

// ProjectNetwork is the network a project's database and apps share. Unlike
// the network every app is on, containers on it can reach each other, and
// only the project's own containers are on it.
func (h *Host) ProjectNetwork(project string) string { return h.owner + "-net-" + project }

func (h *Host) databaseLabels(project string) map[string]string {
	labels := h.Labels()
	labels[databaseLabel] = project
	return labels
}

// EnsureProjectNetwork makes the project's network if it isn't there.
func (h *Host) EnsureProjectNetwork(ctx context.Context, project string) error {
	c, err := h.engine(ctx)
	if err != nil {
		return err
	}
	return h.ensureProjectNetwork(ctx, c, project)
}

func (h *Host) ensureProjectNetwork(ctx context.Context, c *client.Client, project string) error {
	name := h.ProjectNetwork(project)
	netw, err := c.NetworkInspect(ctx, name, client.NetworkInspectOptions{})
	if errdefs.IsNotFound(err) {
		_, err = c.NetworkCreate(ctx, name, client.NetworkCreateOptions{Driver: "bridge", Labels: h.databaseLabels(project)})
		if err != nil && !errdefs.IsConflict(err) {
			return err
		}
		netw, err = c.NetworkInspect(ctx, name, client.NetworkInspectOptions{})
	}
	if err != nil {
		return err
	}
	if netw.Network.Labels[ownerLabel] != h.owner || netw.Network.Labels[databaseLabel] != project {
		return fmt.Errorf("the network %s belongs to something else", name)
	}
	return nil
}

// pullImage fetches an image unless it's already here.
func (h *Host) pullImage(ctx context.Context, c *client.Client, image string) error {
	if _, err := c.ImageInspect(ctx, image); err == nil {
		return nil
	} else if !errdefs.IsNotFound(err) {
		return err
	}
	pull, err := c.ImagePull(ctx, image, client.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("download %s: %w", image, err)
	}
	defer pull.Close()
	if err := pull.Wait(ctx); err != nil {
		return fmt.Errorf("download %s: %w", image, err)
	}
	return nil
}

// StartPostgres makes sure a project's Postgres server runs: it downloads
// the image, makes the network and volume, and creates the container if
// any of them is missing, or starts the container if it's stopped. The
// volume keeps the data whatever happens to the container.
func (h *Host) StartPostgres(ctx context.Context, spec PostgresSpec) error {
	c, err := h.engine(ctx)
	if err != nil {
		return err
	}
	name := h.DatabaseContainer(spec.Project)
	res, err := c.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	if err == nil {
		if res.Container.Config.Labels[ownerLabel] != h.owner || res.Container.Config.Labels[databaseLabel] != spec.Project {
			return fmt.Errorf("the container %s belongs to something else", name)
		}
		if res.Container.State != nil && (res.Container.State.Running || res.Container.State.Restarting) {
			return nil
		}
		_, err = c.ContainerStart(ctx, res.Container.ID, client.ContainerStartOptions{})
		return err
	}
	if !errdefs.IsNotFound(err) {
		return err
	}
	if err := h.pullImage(ctx, c, spec.Image); err != nil {
		return err
	}
	if err := h.ensureProjectNetwork(ctx, c, spec.Project); err != nil {
		return err
	}
	if _, err := c.VolumeInspect(ctx, name, client.VolumeInspectOptions{}); errdefs.IsNotFound(err) {
		if _, err := c.VolumeCreate(ctx, client.VolumeCreateOptions{Name: name, Labels: h.databaseLabels(spec.Project)}); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	cmd := []string{"postgres"}
	for _, s := range spec.Settings {
		cmd = append(cmd, "-c", s)
	}
	stop := 60 // seconds for a clean shutdown, so a restart never needs recovery
	config := &container.Config{
		Image: spec.Image, Cmd: cmd, Labels: h.databaseLabels(spec.Project), StopTimeout: &stop,
		Env: []string{
			"POSTGRES_PASSWORD=" + spec.Password,
			// The builtin C.UTF-8 locale sorts the same on every machine and
			// needs no locale files, which Alpine doesn't have.
			"POSTGRES_INITDB_ARGS=--locale-provider=builtin --builtin-locale=C.UTF-8 --encoding=UTF8",
		},
		Healthcheck: &container.HealthConfig{
			Test:     []string{"CMD", "pg_isready", "-U", "postgres", "-d", "postgres", "-q"},
			Interval: 10 * time.Second, Timeout: 5 * time.Second, Retries: 3, StartPeriod: 60 * time.Second,
		},
	}
	pids := int64(256)
	res2 := container.Resources{PidsLimit: &pids}
	if spec.MemoryMB > 0 {
		res2.Memory = int64(spec.MemoryMB) << 20
		res2.MemorySwap = res2.Memory // no swap: a database that swaps is slower than one that's told no
	}
	host := &container.HostConfig{
		NetworkMode:   container.NetworkMode(h.ProjectNetwork(spec.Project)),
		RestartPolicy: restartPolicy, SecurityOpt: []string{"no-new-privileges"}, Resources: res2,
		// The image keeps its data under /var/lib/postgresql/18/docker, so
		// the volume holds every major version's folder side by side.
		Mounts:    []mount.Mount{{Type: mount.TypeVolume, Source: name, Target: "/var/lib/postgresql"}},
		LogConfig: container.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "10m", "max-file": "2"}},
	}
	netConfig := &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{
		h.ProjectNetwork(spec.Project): {Aliases: []string{DatabaseHost}},
	}}
	created, err := c.ContainerCreate(ctx, client.ContainerCreateOptions{Name: name, Config: config, HostConfig: host, NetworkingConfig: netConfig})
	if err != nil {
		return err
	}
	_, err = c.ContainerStart(ctx, created.ID, client.ContainerStartOptions{})
	return err
}

// WaitPostgres waits until the project's server accepts connections.
func (h *Host) WaitPostgres(ctx context.Context, project string, within time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, within)
	defer cancel()
	for {
		code, _, err := h.Exec(ctx, h.DatabaseContainer(project), []string{"pg_isready", "-U", "postgres", "-d", "postgres", "-q"}, nil, io.Discard)
		if err == nil && code == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			if err == nil {
				err = errors.New("it didn't start accepting connections")
			}
			return fmt.Errorf("the database didn't start in %s: %w", within, err)
		case <-time.After(time.Second):
		}
	}
}

// RemovePostgres removes a project's Postgres container, and with
// withData its volume and network too, which deletes its data for good.
func (h *Host) RemovePostgres(ctx context.Context, project string, withData bool) error {
	c, err := h.engine(ctx)
	if err != nil {
		return err
	}
	name := h.DatabaseContainer(project)
	res, err := c.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	if err == nil {
		if res.Container.Config.Labels[ownerLabel] != h.owner || res.Container.Config.Labels[databaseLabel] != project {
			return fmt.Errorf("the container %s belongs to something else", name)
		}
		stop := 60
		c.ContainerStop(ctx, res.Container.ID, client.ContainerStopOptions{Timeout: &stop})
		if _, err := c.ContainerRemove(ctx, res.Container.ID, client.ContainerRemoveOptions{Force: true}); err != nil && !errdefs.IsNotFound(err) {
			return err
		}
	} else if !errdefs.IsNotFound(err) {
		return err
	}
	if !withData {
		return nil
	}
	vol, err := c.VolumeInspect(ctx, name, client.VolumeInspectOptions{})
	if err == nil {
		if vol.Volume.Labels[ownerLabel] != h.owner || vol.Volume.Labels[databaseLabel] != project {
			return fmt.Errorf("the volume %s belongs to something else", name)
		}
		if _, err := c.VolumeRemove(ctx, name, client.VolumeRemoveOptions{}); err != nil && !errdefs.IsNotFound(err) {
			return err
		}
	} else if !errdefs.IsNotFound(err) {
		return err
	}
	return h.removeProjectNetwork(ctx, c, project)
}

// removeProjectNetwork removes the project's network, first taking off any
// app still connected to it.
func (h *Host) removeProjectNetwork(ctx context.Context, c *client.Client, project string) error {
	name := h.ProjectNetwork(project)
	netw, err := c.NetworkInspect(ctx, name, client.NetworkInspectOptions{})
	if errdefs.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if netw.Network.Labels[ownerLabel] != h.owner || netw.Network.Labels[databaseLabel] != project {
		return fmt.Errorf("the network %s belongs to something else", name)
	}
	for id := range netw.Network.Containers {
		c.NetworkDisconnect(ctx, name, client.NetworkDisconnectOptions{Container: id, Force: true})
	}
	if _, err := c.NetworkRemove(ctx, name, client.NetworkRemoveOptions{}); err != nil && !errdefs.IsNotFound(err) {
		return err
	}
	return nil
}

// PostgresState inspects a project's Postgres container and reads how much
// memory it uses.
func (h *Host) PostgresState(ctx context.Context, project string) (DatabaseState, error) {
	c, err := h.engine(ctx)
	if err != nil {
		return DatabaseState{}, err
	}
	res, err := c.ContainerInspect(ctx, h.DatabaseContainer(project), client.ContainerInspectOptions{})
	if errdefs.IsNotFound(err) {
		return DatabaseState{State: "missing"}, nil
	}
	if err != nil {
		return DatabaseState{}, err
	}
	app := res.Container
	if app.Config.Labels[ownerLabel] != h.owner || app.Config.Labels[databaseLabel] != project {
		return DatabaseState{}, errors.New("the database container belongs to something else")
	}
	st := DatabaseState{State: "exited", Restarts: app.RestartCount}
	if app.State != nil {
		st.State = string(app.State.Status)
		st.OOMKilled = app.State.OOMKilled
		if t, err := time.Parse(time.RFC3339Nano, app.State.StartedAt); err == nil && app.State.Running {
			st.StartedAt = t
		}
		if app.State.Health != nil {
			st.Health = string(app.State.Health.Status)
		}
	}
	if app.HostConfig != nil && app.HostConfig.Memory > 0 {
		st.LimitBytes = uint64(app.HostConfig.Memory)
	}
	if st.State == "running" {
		if used, err := h.memoryInUse(ctx, c, app.ID); err == nil {
			st.MemoryBytes = used
		}
	}
	return st, nil
}

// memoryInUse is what the container uses, less the files the kernel keeps
// in its page cache, as docker stats counts it.
func (h *Host) memoryInUse(ctx context.Context, c *client.Client, id string) (uint64, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	res, err := c.ContainerStats(ctx, id, client.ContainerStatsOptions{Stream: false, IncludePreviousSample: false})
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	var s container.StatsResponse
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&s); err != nil {
		return 0, err
	}
	used := s.MemoryStats.Usage
	if cache, ok := s.MemoryStats.Stats["inactive_file"]; ok && cache < used {
		used -= cache
	}
	return used, nil
}

// Exec runs a command in a running container as its default user, feeding
// it stdin if there is one and writing what it prints to stdout to stdout.
// It returns the command's exit code and the end of what it printed to
// stderr.
func (h *Host) Exec(ctx context.Context, name string, cmd []string, stdin io.Reader, stdout io.Writer) (int, string, error) {
	c, err := h.engine(ctx)
	if err != nil {
		return -1, "", err
	}
	res, err := c.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	if err != nil {
		return -1, "", err
	}
	if res.Container.Config.Labels[ownerLabel] != h.owner {
		return -1, "", errors.New("refusing to run a command in a container owned by another instance")
	}
	created, err := c.ExecCreate(ctx, res.Container.ID, client.ExecCreateOptions{
		Cmd: cmd, AttachStdout: true, AttachStderr: true, AttachStdin: stdin != nil,
	})
	if err != nil {
		return -1, "", err
	}
	att, err := c.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		return -1, "", err
	}
	defer att.Close()
	fed := make(chan error, 1)
	if stdin != nil {
		go func() {
			_, err := io.Copy(att.Conn, stdin)
			if cerr := att.CloseWrite(); err == nil {
				err = cerr
			}
			fed <- err
		}()
	} else {
		fed <- nil
	}
	// Stop reading when ctx ends, or a command that never ends would hold
	// this forever.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			att.Close()
		case <-done:
		}
	}()
	stderr := &tail{max: 4 << 10}
	if _, err := stdcopy.StdCopy(stdout, stderr, att.Reader); err != nil {
		if ctx.Err() != nil {
			return -1, stderr.String(), ctx.Err()
		}
		return -1, stderr.String(), err
	}
	if err := <-fed; err != nil && !errors.Is(err, io.ErrClosedPipe) {
		return -1, stderr.String(), fmt.Errorf("send input: %w", err)
	}
	for range 50 {
		ins, err := c.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
		if err != nil {
			return -1, stderr.String(), err
		}
		if !ins.Running {
			return ins.ExitCode, stderr.String(), nil
		}
		select {
		case <-ctx.Done():
			return -1, stderr.String(), ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return -1, stderr.String(), errors.New("the command's exit code never came")
}

// tail keeps the last max bytes written to it.
type tail struct {
	max int
	buf bytes.Buffer
}

func (t *tail) Write(p []byte) (int, error) {
	t.buf.Write(p)
	if over := t.buf.Len() - t.max; over > 0 {
		t.buf.Next(over)
	}
	return len(p), nil
}

func (t *tail) String() string { return strings.TrimSpace(t.buf.String()) }

// Databases lists the projects that have a Postgres container here, so
// pierd can tell which ones no project owns any more.
func (h *Host) Databases(ctx context.Context) (map[string]bool, error) {
	c, err := h.engine(ctx)
	if err != nil {
		return nil, err
	}
	list, err := c.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: client.Filters{}.Add("label", ownerLabel+"="+h.owner).Add("label", databaseLabel)})
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, item := range list.Items {
		if p := item.Labels[databaseLabel]; p != "" {
			out[p] = true
		}
	}
	return out, nil
}

// Memory sizes for a database container, in MB, from smallest.
var DatabaseMemoryChoices = []int{128, 256, 512, 1024, 2048, 4096}

// mb writes a size in MB as Postgres reads it.
func mb(n int) string { return strconv.Itoa(n) + "MB" }

// PostgresSettings sizes a server's memory settings from its container's
// limit, and sets everything else for a small server that should use as
// little memory and disk as it can and still be safe:
//
//   - shared_buffers is a quarter of the limit, and the cache Postgres
//     assumes the kernel keeps for it half;
//   - few background workers and parallel workers, and JIT off, which costs
//     memory and start-up time and rarely helps small queries;
//   - WAL is compressed with zstd and kept small (Pier backs up with
//     pg_dump, so wal_level is minimal and there are no replicas to keep
//     WAL for), and large values are compressed with lz4;
//   - a transaction left open by a crashed client ends after ten minutes,
//     so it can't hold locks or block vacuum forever.
func PostgresSettings(memoryMB int) []string {
	if memoryMB <= 0 {
		memoryMB = 256
	}
	clamp := func(n, lo, hi int) int { return max(lo, min(n, hi)) }
	connections := clamp(memoryMB/4, 25, 100)
	return []string{
		"shared_buffers=" + mb(clamp(memoryMB/4, 16, 1024)),
		"effective_cache_size=" + mb(clamp(memoryMB/2, 32, 3072)),
		"work_mem=" + mb(clamp(memoryMB/64, 1, 32)),
		"maintenance_work_mem=" + mb(clamp(memoryMB/16, 16, 256)),
		"max_connections=" + strconv.Itoa(connections),
		"jit=off",
		"huge_pages=off",
		"max_worker_processes=4",
		"max_parallel_workers=2",
		"max_parallel_workers_per_gather=1",
		"max_parallel_maintenance_workers=1",
		"autovacuum_max_workers=2",
		"io_workers=2",
		"wal_level=minimal",
		"max_wal_senders=0",
		"wal_compression=zstd",
		"max_wal_size=256MB",
		"min_wal_size=32MB",
		"checkpoint_timeout=15min",
		"default_toast_compression=lz4",
		"idle_in_transaction_session_timeout=10min",
		"log_min_duration_statement=-1",
	}
}
