package apphost

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// jobLabel marks a container that runs one run of a scheduled job, with
// the run's ID, so one left behind by a stop can be found and removed.
const jobLabel = "io.pier.job"

// JobSpec is a command to run once, in a fresh container from a server
// deployment's image.
type JobSpec struct {
	App      string   // the deployment's container; its image is App+":app"
	Command  string   // run with /bin/sh -c
	Env      []string // the project's variables
	MemoryMB int      // as the app's: 0 is DefaultMemoryMB, negative is no limit
	CPUs     float64
	Run      string // the run's ID, for the label
	Networks []string // joined as well, as the app does: the project's, when it has a database
}

// ErrNoImage means the deployment's image is gone, so nothing can run in it.
var ErrNoImage = errors.New("production's image isn't on this server any more")

// RunJob runs a command in a fresh container from a deployment's image, with
// the same memory and CPU limits as the app, and sends each line it prints
// to emit as it comes. It returns the command's exit code. When ctx ends
// first, the container is killed and ctx's error is returned. The container
// is removed either way.
func (h *Host) RunJob(ctx context.Context, spec JobSpec, emit func(LogLine) error) (int, error) {
	c, err := h.engine(ctx)
	if err != nil {
		return -1, err
	}
	img, err := c.ImageInspect(ctx, spec.App+":app")
	if errdefs.IsNotFound(err) {
		return -1, ErrNoImage
	}
	if err != nil {
		return -1, err
	}
	if err := h.ensureNetwork(ctx, c); err != nil {
		return -1, err
	}
	labels := h.Labels()
	labels[jobLabel] = spec.Run
	config := &container.Config{
		Env: spec.Env, Labels: labels,
		Entrypoint: []string{"/bin/sh", "-c"}, Cmd: []string{spec.Command},
	}
	host := &container.HostConfig{
		NetworkMode: container.NetworkMode(h.owner), SecurityOpt: []string{"no-new-privileges"},
		Resources: resolveResources(StartOptions{MemoryMB: spec.MemoryMB, CPUs: spec.CPUs}),
		LogConfig: container.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "10m", "max-file": "1"}},
	}
	name := "pier-job-" + strings.ToLower(rand.Text())[:12]
	created, err := c.ContainerCreate(ctx, client.ContainerCreateOptions{Name: name, Image: img.ID, Config: config, HostConfig: host})
	if err != nil {
		return -1, err
	}
	// The container goes whatever happens, even once ctx has ended.
	defer func() {
		gone, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		c.ContainerRemove(gone, created.ID, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
	}()
	if err := h.joinNetworks(ctx, c, created.ID, spec.Networks); err != nil {
		return -1, err
	}
	// Waiting starts before the start, so a command that exits at once
	// isn't missed.
	waiting, stopWaiting := context.WithCancel(context.WithoutCancel(ctx))
	defer stopWaiting()
	wait := c.ContainerWait(waiting, created.ID, client.ContainerWaitOptions{Condition: container.WaitConditionNextExit})
	if _, err := c.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		return -1, err
	}
	logs, err := c.ContainerLogs(waiting, created.ID, client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true, Follow: true})
	if err != nil {
		return -1, err
	}
	followed := make(chan error, 1)
	go func() {
		defer logs.Close()
		followed <- FollowLogLines(logs, false, emit)
	}()
	// The output ends soon after the process does; it's waited for, but
	// not forever.
	drain := func() {
		select {
		case <-followed:
		case <-time.After(5 * time.Second):
		}
	}
	select {
	case res := <-wait.Result:
		drain()
		if res.Error != nil && res.Error.Message != "" {
			return int(res.StatusCode), fmt.Errorf("Docker: %s", res.Error.Message)
		}
		return int(res.StatusCode), nil
	case err := <-wait.Error:
		return -1, err
	case <-ctx.Done():
		kill, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		c.ContainerKill(kill, created.ID, client.ContainerKillOptions{Signal: "KILL"})
		code := -1
		select {
		case res := <-wait.Result:
			code = int(res.StatusCode)
		case <-time.After(10 * time.Second):
		}
		drain()
		return code, ctx.Err()
	}
}

// RemoveJobs removes the containers of runs that pierd was in the middle of
// when it stopped.
func (h *Host) RemoveJobs(ctx context.Context) error {
	c, err := h.engine(ctx)
	if err != nil {
		return err
	}
	list, err := c.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: client.Filters{}.Add("label", ownerLabel+"="+h.owner).Add("label", jobLabel)})
	if err != nil {
		return err
	}
	for _, j := range list.Items {
		if _, err := c.ContainerRemove(ctx, j.ID, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true}); err != nil && !errdefs.IsNotFound(err) {
			return err
		}
	}
	return nil
}
