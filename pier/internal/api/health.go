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
	ctx, stop := context.WithTimeout(ctx, 3*time.Second)
	defer stop()
	b, err := exec.CommandContext(ctx, "docker", args...).Output()
	return strings.TrimSpace(string(b)), err
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
	if b, ok := d.Runner.(build.Docker); ok {
		version, e := dockerOutput(ctx, "version", "--format", "{{.Server.Version}}")
		if e == nil {
			svc["Docker"] = version
		} else {
			svc["Docker"] = "Unavailable"
		}
		cache, e := dockerOutput(ctx, "volume", "inspect", "--format", "{{.Mountpoint}}", b.CacheVolume)
		var size *int64
		if e == nil && filepath.IsAbs(cache) {
			if _, statErr := os.Stat(cache); statErr == nil {
				size = treeBytes(cache)
			}
		}
		if size == nil && e == nil {
			// Docker Desktop and a non-root Linux service cannot walk Docker's mountpoint.
			out, err := dockerOutput(ctx, "run", "--pull=never", "--rm", "--network", "none", "--read-only", "-v", b.CacheVolume+":/cache:ro", "--entrypoint", "du", b.Image, "-sk", "/cache")
			if fields := strings.Fields(out); err == nil && len(fields) > 0 {
				if kib, err := strconv.ParseInt(fields[0], 10, 64); err == nil {
					bytes := kib * 1024
					size = &bytes
				}
			}
		}
		items = append(items, diskItem{"Build cache", size})
		h["cache"] = true
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

func (a *API) finishFiles(ctx context.Context) error {
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
		if _, err := dockerOutput(ctx, "volume", "rm", b.CacheVolume); err != nil {
			writeJSON(w, 409, map[string]string{"error": "Docker could not remove Pier’s cache. It may be in use or already empty."})
			return
		}
	default:
		writeJSON(w, 404, map[string]string{"error": "Unknown cleanup operation."})
		return
	}
	a.event(ctx, "", "Cleaned up "+r.PathValue("kind"), "", "")
	writeJSON(w, 200, map[string]bool{"done": true})
}
