package api

import (
	"context"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	"pier/internal/store"
)

func (a *API) SampleServer(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		if err := a.sample(ctx); err != nil && ctx.Err() == nil {
			a.Log.Warn("sample server health", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
func (a *API) sample(ctx context.Context) error {
	cpuUse, err := cpu.PercentWithContext(ctx, 250*time.Millisecond, false)
	if err != nil || len(cpuUse) == 0 {
		return err
	}
	m, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return err
	}
	d, err := disk.UsageWithContext(ctx, filepath.Dir(a.Deployer.Deployments))
	if err != nil {
		return err
	}
	return a.Store.RecordSample(ctx, store.ServerSample{At: time.Now().Truncate(time.Minute).UnixMilli(), CPU: cpuUse[0], Memory: 100 * (1 - float64(m.Available)/float64(m.Total)), Disk: d.UsedPercent})
}
func (a *API) history(w http.ResponseWriter, r *http.Request) {
	span, bucket := 24*time.Hour, 15*time.Minute
	switch r.URL.Query().Get("range") {
	case "7d":
		span, bucket = 7*24*time.Hour, time.Hour
	case "latest":
		span, bucket = 2*time.Minute, time.Minute
	}
	end := time.Now().UnixMilli()
	start := end - span.Milliseconds()
	// The minutes around one moment, like when a trace was slow.
	if at, err := strconv.ParseInt(r.URL.Query().Get("at"), 10, 64); err == nil && at > end-7*24*time.Hour.Milliseconds() && at <= end {
		start, end, bucket = at-5*time.Minute.Milliseconds(), min(end, at+5*time.Minute.Milliseconds()), time.Minute
	}
	samples, err := a.Store.Samples(r.Context(), start, bucket)
	if err != nil {
		a.fail(w, err)
		return
	}
	kept := samples[:0]
	for _, s := range samples {
		if s.At <= end {
			kept = append(kept, s)
		}
	}
	samples = kept
	writeJSON(w, 200, map[string]any{"samples": samples, "start": start, "end": end, "bucket": bucket.Milliseconds()})
}
