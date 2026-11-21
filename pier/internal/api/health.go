package api

import (
	"context"
	"errors"
	"fmt"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"pier/internal/build"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type diskItem struct {
	Name  string `json:"name"`
	Bytes *int64 `json:"bytes"`
}

func treeBytes(root string) *int64 {
	var total int64
	err := filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.Type().IsRegular() {
			f, err := e.Info()
			if err != nil {
				return err
			}
			total += f.Size()
		}
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return &total
}
func dockerOutput(ctx context.Context, args ...string) (string, error) {
	return dockerOutputTimeout(ctx, 3*time.Second, args...)
}
func dockerOutputTimeout(ctx context.Context, timeout time.Duration, args ...string) (string, error) {
	ctx, stop := context.WithTimeout(ctx, timeout)
	defer stop()
	b, err := exec.CommandContext(ctx, "docker", args...).Output()
	return strings.TrimSpace(string(b)), err
}

var dockerSizeRe = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)\s*([A-Za-z]+)$`)

func parseDockerSize(s string) (int64, bool) {
	m := dockerSizeRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	mult := map[string]float64{
		"b": 1, "kb": 1e3, "mb": 1e6, "gb": 1e9, "tb": 1e12,
		"kib": 1024, "mib": 1024 * 1024, "gib": 1024 * 1024 * 1024, "tib": 1024 * 1024 * 1024 * 1024,
	}
	u, ok := mult[strings.ToLower(m[2])]
	if !ok {
		return 0, false
	}
	return int64(v * u), true
}

func dockerDiskUsage(ctx context.Context) (images, buildCache *int64) {
	out, err := dockerOutputTimeout(ctx, 5*time.Second, "system", "df", "--format", "{{.Type}}\t{{.Size}}")
	if err != nil {
		return nil, nil
	}
	for _, line := range strings.Split(out, "\n") {
		kind, rawSize, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok {
			continue
		}
		bytes, ok := parseDockerSize(rawSize)
		if !ok {
			continue
		}
		switch kind {
		case "Images":
			v := bytes
			images = &v
		case "Build Cache":
			v := bytes
			buildCache = &v
		}
	}
	return images, buildCache
}

// meter remembers a size that is slow to measure. The Server page asks
// every few seconds for as long as it's open; the measuring happens behind
// it, one at a time and only now and then, and the page gets the last answer.
type meter struct {
	every time.Duration // how long a measurement stands before the next
	wait  time.Duration // how long a request waits when there's no answer yet

	mu      sync.Mutex
	bytes   *int64        // the last measurement; nil if it failed or none has ended
	at      time.Time     // when it ended
	running chan struct{} // closed when the measurement under way ends; nil for none
	gen     int           // forget moves it on, to drop a measurement of what's gone
}

// size returns the last measurement, and starts the next in the background
// when that one is due and none is running. With nothing to show yet it
// gives a quick measurement a moment to answer this request.
func (m *meter) size(ctx context.Context, measure func() *int64) *int64 {
	m.mu.Lock()
	if m.running == nil && (m.at.IsZero() || time.Since(m.at) >= m.every) {
		done, gen := make(chan struct{}), m.gen
		m.running = done
		go func() {
			v := measure()
			m.mu.Lock()
			if gen == m.gen {
				m.bytes, m.at = v, time.Now()
			}
			m.running = nil
			m.mu.Unlock()
			close(done)
		}()
	}
	known, running := m.bytes, m.running
	m.mu.Unlock()
	if known != nil || running == nil {
		return known
	}
	wait := time.NewTimer(m.wait)
	defer wait.Stop()
	select {
	case <-running:
	case <-wait.C:
	case <-ctx.Done():
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bytes
}

// forget drops the measurement, so the next request measures again.
func (m *meter) forget() {
	m.mu.Lock()
	m.bytes, m.at = nil, time.Time{}
	m.gen++
	m.mu.Unlock()
}

// cacheMeasureLimit is how long adding up the build cache may take. A cache
// of many gigabytes takes far longer than a page should wait, which is why
// it's measured behind the page.
const cacheMeasureLimit = 2 * time.Minute

// cacheSize is the size of the build cache volume. pierd has one builder,
// so there is one of these.
var cacheSize = &meter{every: 5 * time.Minute, wait: 2 * time.Second}

// cacheMeasurer names the container that measures a cache volume. The name
// is how it's stopped, and Docker won't start a second one under it.
func cacheMeasurer(volume string) string { return volume + "-size" }

// measureCache adds up the build cache volume, or returns nil if it can't
// within limit. pierd walks Docker's folder itself where it may; Docker
// Desktop and a service that doesn't run as root can't, and there a
// throwaway container runs du over the volume. Giving up on the docker
// command doesn't stop the container it started, so that is removed by name.
func measureCache(volume, image string, limit time.Duration) *int64 {
	ctx, stop := context.WithTimeout(context.Background(), limit)
	defer stop()
	mount, err := dockerOutput(ctx, "volume", "inspect", "--format", "{{.Mountpoint}}", volume)
	if err != nil {
		return nil
	}
	if filepath.IsAbs(mount) {
		if _, err := os.Stat(mount); err == nil {
			if size := treeBytes(mount); size != nil {
				return size
			}
		}
	}
	name := cacheMeasurer(volume)
	out, err := dockerOutputTimeout(ctx, limit, "run", "--name", name, "--pull=never", "--rm", "--network", "none", "--read-only", "-v", volume+":/cache:ro", "--entrypoint", "du", image, "-sk", "/cache")
	if err != nil {
		_, _ = dockerOutputTimeout(context.Background(), 10*time.Second, "rm", "-f", name)
		return nil
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return nil
	}
	kib, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return nil
	}
	bytes := kib * 1024
	return &bytes
}

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d := a.Deployer
	root := filepath.Dir(d.Deployments)
	// Coalesce concurrent dashboard polls and directory walks.
	a.healthMu.Lock()
	defer a.healthMu.Unlock()
	if time.Since(a.healthAt) < 10*time.Second && a.healthSnapshot != nil {
		writeJSON(w, 200, a.healthSnapshot)
		return
	}
	usage, err := disk.UsageWithContext(ctx, root)
	if err != nil {
		a.fail(w, err)
		return
	}
	h := map[string]any{"os": runtime.GOOS, "arch": runtime.GOARCH, "cores": runtime.NumCPU(), "disk_total": usage.Total, "disk_free": usage.Free, "disk_percent": usage.UsedPercent, "address": d.PierHost, "started_at": a.StartedAt}
	if m, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		h["memory_total"] = m.Total
		h["memory_used"] = m.Total - m.Available
	}
	if pc, err := cpu.PercentWithContext(ctx, 150*time.Millisecond, false); err == nil && len(pc) > 0 {
		h["cpu"] = pc[0]
	}
	var items []diskItem
	for _, x := range []struct{ name, path string }{{"Deployments", d.Deployments}, {"Source archives", d.Sources}, {"Repositories", d.Repos}, {"Build logs", d.Logs}, {"Request database", filepath.Join(root, "requests.db")}} {
		items = append(items, diskItem{x.name, treeBytes(x.path)})
	}
	svc := map[string]string{"pierd": "Running", "Caddy": "Unavailable", "Docker": "Unused (local builder)"}
	if c, e := net.DialTimeout("tcp", a.SiteListener, time.Second); e == nil {
		svc["Caddy"] = "Running"
		c.Close()
	}
	b, dockerBuilds := d.Runner.(build.Docker)
	if dockerBuilds || d.Apps != nil {
		version, e := dockerOutput(ctx, "version", "--format", "{{.Server.Version}}")
		if e == nil {
			svc["Docker"] = version
		} else {
			svc["Docker"] = "Unavailable"
		}
	}
	if dockerBuilds {
		size := cacheSize.size(ctx, func() *int64 { return measureCache(b.CacheVolume, b.Image, cacheMeasureLimit) })
		items = append(items, diskItem{"Build cache", size})
		h["cache"] = true
	}
	if svc["Docker"] != "Unavailable" && !strings.HasPrefix(svc["Docker"], "Unused") {
		imgs, bcache := dockerDiskUsage(ctx)
		if imgs != nil {
			items = append(items, diskItem{"Docker images", imgs})
		}
		if bcache != nil {
			items = append(items, diskItem{"Docker build cache", bcache})
			h["docker_cache"] = true
		}
	}
	count, err := a.Store.CleanupCount(ctx)
	if err != nil {
		a.fail(w, err)
		return
	}
	h["previews"] = count
	h["services"] = svc
	h["items"] = items
	a.healthSnapshot = h
	a.healthAt = time.Now()
	writeJSON(w, 200, h)
}

var artifactID = regexp.MustCompile("^[0-9a-f]{6}$")

// finishFiles removes the files of the deployments queued for clean-up.
// Their containers and images are removed first, but files don't wait for
// that to work: the deployments' rows are gone already, so nothing else
// would ever remove these files, and with Docker away they'd sit on the disk
// and in the Server page's count. pierd tries the containers again every
// minute.
func (a *API) finishFiles(ctx context.Context) error {
	if err := a.Deployer.CleanupApps(ctx); err != nil {
		a.Log.Warn("application cleanup deferred", "err", err)
	}
	ids, err := a.Store.CleanupFiles(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if !artifactID.MatchString(id) {
			return fmt.Errorf("invalid cleanup ID")
		}
		for _, path := range []string{filepath.Join(a.Deployer.Deployments, id), filepath.Join(a.Deployer.Sources, id+".tar.gz"), filepath.Join(a.Deployer.Logs, id+".log")} {
			if err := os.RemoveAll(path); err != nil {
				return err
			}
		}
		if err := a.Store.FinishCleanup(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// ResumeCleanup finishes only work already committed by the owner.
func (a *API) ResumeCleanup(ctx context.Context) error { return a.finishFiles(ctx) }
func (a *API) cleanup(w http.ResponseWriter, r *http.Request) {
	a.healthMu.Lock()
	defer a.healthMu.Unlock()
	a.healthSnapshot = nil
	ctx := r.Context()
	switch r.PathValue("kind") {
	case "previews":
		if err := a.Store.QueuePreviewCleanup(ctx); err != nil {
			a.fail(w, err)
			return
		}
		if err := a.Deployer.Sync(ctx); err != nil {
			a.fail(w, err)
			return
		}
		if err := a.finishFiles(ctx); err != nil {
			a.fail(w, err)
			return
		}
	case "cache":
		b, ok := a.Deployer.Runner.(build.Docker)
		if !ok || b.CacheVolume == "" {
			writeJSON(w, 409, map[string]string{"error": "This builder has no Docker cache."})
			return
		}
		// Docker won't remove a volume that a container has mounted, and the
		// one measuring it would be the reason more often than a build.
		_, _ = dockerOutput(ctx, "rm", "-f", cacheMeasurer(b.CacheVolume))
		if _, err := dockerOutput(ctx, "volume", "rm", b.CacheVolume); err != nil {
			writeJSON(w, 409, map[string]string{"error": "Docker could not remove Pier’s cache. It may be in use or already empty."})
			return
		}
		cacheSize.forget()
	case "docker-cache":
		if _, err := dockerOutputTimeout(ctx, 60*time.Second, "buildx", "prune", "-af"); err != nil {
			writeJSON(w, 409, map[string]string{"error": "Docker could not prune the Buildx cache."})
			return
		}
		_, _ = dockerOutputTimeout(ctx, 30*time.Second, "image", "prune", "-f")
	default:
		writeJSON(w, 404, map[string]string{"error": "Unknown cleanup operation."})
		return
	}
	a.event(ctx, "", "Cleaned up "+r.PathValue("kind"), "", "")
	writeJSON(w, 200, map[string]bool{"done": true})
}
