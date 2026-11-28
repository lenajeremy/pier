package apphost

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

// The telemetry stack is a few servers pierd runs for the whole machine:
// Tempo keeps traces, Loki logs, Prometheus metrics. Each has its data on a
// volume of its own, its settings in files pierd writes, and its ports on
// loopback, where only pierd reaches them; apps send to pierd, which says
// which project the data belongs to.

const (
	telemetryLabel = "io.pier.telemetry"
	configLabel    = "io.pier.config" // the settings' hash; new settings make a new container
)

// ServiceSpec is one of the telemetry stack's servers.
type ServiceSpec struct {
	Name     string            // tempo, loki, prometheus
	Image    string            // pinned, like grafana/tempo:2.8.2
	Cmd      []string          // the server's arguments
	Ports    []int             // the container's ports pierd reaches
	Files    map[string]string // a file on this machine -> where the server reads it
	Data     string            // where the server keeps its data, on the volume
	Env      []string
	MemoryMB int
	// Shared puts the server on the stack's own network, where the others
	// reach it by its name: Grafana reads Tempo, Loki and Prometheus there.
	Shared bool
	Config string // a hash of everything above that should restart it when it changes
}

// ServiceContainer is the name of one of the stack's containers. Its
// volume has the same name.
func (h *Host) ServiceContainer(name string) string { return h.owner + "-telemetry-" + name }

// TelemetryNetwork is the network the stack's servers share.
func (h *Host) TelemetryNetwork() string { return h.owner + "-telemetry" }

func (h *Host) ensureTelemetryNetwork(ctx context.Context, c *client.Client) error {
	name := h.TelemetryNetwork()
	netw, err := c.NetworkInspect(ctx, name, client.NetworkInspectOptions{})
	if errdefs.IsNotFound(err) {
		labels := h.Labels()
		labels[telemetryLabel] = "network"
		_, err = c.NetworkCreate(ctx, name, client.NetworkCreateOptions{Driver: "bridge", Labels: labels})
		if err != nil && !errdefs.IsConflict(err) {
			return err
		}
		netw, err = c.NetworkInspect(ctx, name, client.NetworkInspectOptions{})
	}
	if err != nil {
		return err
	}
	if netw.Network.Labels[ownerLabel] != h.owner || netw.Network.Labels[telemetryLabel] != "network" {
		return fmt.Errorf("the network %s belongs to something else", name)
	}
	return nil
}

// StartService makes sure one server of the stack runs with these settings:
// it downloads the image and makes the volume if they're missing, replaces
// the container if its settings changed, and starts it if it's stopped.
func (h *Host) StartService(ctx context.Context, spec ServiceSpec) error {
	c, err := h.engine(ctx)
	if err != nil {
		return err
	}
	name := h.ServiceContainer(spec.Name)
	res, err := c.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	if err == nil {
		labels := res.Container.Config.Labels
		if labels[ownerLabel] != h.owner || labels[telemetryLabel] != spec.Name {
			return fmt.Errorf("the container %s belongs to something else", name)
		}
		if labels[configLabel] == spec.Config {
			if res.Container.State != nil && (res.Container.State.Running || res.Container.State.Restarting) {
				return nil
			}
			_, err = c.ContainerStart(ctx, res.Container.ID, client.ContainerStartOptions{})
			return err
		}
		if _, err := c.ContainerRemove(ctx, res.Container.ID, client.ContainerRemoveOptions{Force: true}); err != nil {
			return err
		}
	} else if !errdefs.IsNotFound(err) {
		return err
	}
	if err := h.pullImage(ctx, c, spec.Image); err != nil {
		return err
	}
	labels := map[string]string{ownerLabel: h.owner, telemetryLabel: spec.Name, configLabel: spec.Config}
	if _, err := c.VolumeInspect(ctx, name, client.VolumeInspectOptions{}); errdefs.IsNotFound(err) {
		if _, err := c.VolumeCreate(ctx, client.VolumeCreateOptions{Name: name, Labels: labels}); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	exposed := network.PortSet{}
	bindings := network.PortMap{}
	for _, p := range spec.Ports {
		port, _ := network.PortFrom(uint16(p), network.TCP)
		free, err := freePort()
		if err != nil {
			return err
		}
		exposed[port] = struct{}{}
		bindings[port] = []network.PortBinding{{HostIP: loopback, HostPort: free}}
	}
	mounts := []mount.Mount{{Type: mount.TypeVolume, Source: name, Target: spec.Data}}
	for src, dst := range spec.Files {
		mounts = append(mounts, mount.Mount{Type: mount.TypeBind, Source: src, Target: dst, ReadOnly: true})
	}
	stop := 30
	pids := int64(512)
	resources := container.Resources{PidsLimit: &pids}
	if spec.MemoryMB > 0 {
		resources.Memory = int64(spec.MemoryMB) << 20
	}
	config := &container.Config{Image: spec.Image, Cmd: spec.Cmd, Env: spec.Env, Labels: labels, StopTimeout: &stop, ExposedPorts: exposed}
	host := &container.HostConfig{
		PortBindings: bindings, RestartPolicy: restartPolicy, SecurityOpt: []string{"no-new-privileges"}, Resources: resources, Mounts: mounts,
		LogConfig: container.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "10m", "max-file": "2"}},
	}
	var netConfig *network.NetworkingConfig
	if spec.Shared {
		if err := h.ensureTelemetryNetwork(ctx, c); err != nil {
			return err
		}
		host.NetworkMode = container.NetworkMode(h.TelemetryNetwork())
		netConfig = &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{h.TelemetryNetwork(): {Aliases: []string{spec.Name}}}}
	}
	created, err := c.ContainerCreate(ctx, client.ContainerCreateOptions{Name: name, Config: config, HostConfig: host, NetworkingConfig: netConfig})
	if err != nil {
		return err
	}
	_, err = c.ContainerStart(ctx, created.ID, client.ContainerStartOptions{})
	return err
}

func freePort() (string, error) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	return port, l.Close()
}

// ServiceAddress is where pierd reaches one of a server's ports.
func (h *Host) ServiceAddress(ctx context.Context, name string, port int) (string, error) {
	c, err := h.engine(ctx)
	if err != nil {
		return "", err
	}
	res, err := c.ContainerInspect(ctx, h.ServiceContainer(name), client.ContainerInspectOptions{})
	if err != nil {
		return "", err
	}
	if res.Container.Config.Labels[ownerLabel] != h.owner || res.Container.Config.Labels[telemetryLabel] != name {
		return "", errors.New("the container belongs to something else")
	}
	want, _ := network.PortFrom(uint16(port), network.TCP)
	if res.Container.NetworkSettings != nil {
		for _, b := range res.Container.NetworkSettings.Ports[want] {
			if b.HostIP == loopback && b.HostPort != "" {
				return net.JoinHostPort("127.0.0.1", b.HostPort), nil
			}
		}
	}
	return "", fmt.Errorf("%s isn't running", name)
}

// ServiceState is how one of the stack's servers is doing.
type ServiceState struct {
	State       string    `json:"state"` // running, restarting, exited or missing
	StartedAt   time.Time `json:"started_at,omitzero"`
	MemoryBytes uint64    `json:"memory_bytes,omitempty"`
}

// ServiceStatus reads one server's state and memory.
func (h *Host) ServiceStatus(ctx context.Context, name string) (ServiceState, error) {
	c, err := h.engine(ctx)
	if err != nil {
		return ServiceState{}, err
	}
	res, err := c.ContainerInspect(ctx, h.ServiceContainer(name), client.ContainerInspectOptions{})
	if errdefs.IsNotFound(err) {
		return ServiceState{State: "missing"}, nil
	}
	if err != nil {
		return ServiceState{}, err
	}
	s := ServiceState{State: "exited"}
	if st := res.Container.State; st != nil {
		switch {
		case st.Running:
			s.State = "running"
		case st.Restarting:
			s.State = "restarting"
		}
		if t, err := time.Parse(time.RFC3339Nano, st.StartedAt); err == nil {
			s.StartedAt = t
		}
	}
	if s.State == "running" {
		if used, err := h.memoryInUse(ctx, c, res.Container.ID); err == nil {
			s.MemoryBytes = used
		}
	}
	return s, nil
}

// StopService stops one of the stack's servers and keeps its data.
func (h *Host) StopService(ctx context.Context, name string) error {
	c, err := h.engine(ctx)
	if err != nil {
		return err
	}
	res, err := c.ContainerInspect(ctx, h.ServiceContainer(name), client.ContainerInspectOptions{})
	if errdefs.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if res.Container.Config.Labels[ownerLabel] != h.owner {
		return errors.New("refusing to stop a container owned by another instance")
	}
	_, err = c.ContainerStop(ctx, res.Container.ID, client.ContainerStopOptions{})
	return err
}
