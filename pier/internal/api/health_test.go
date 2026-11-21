package api

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"pier/internal/deploy"
	"pier/internal/store"
)

func TestParseDockerSize(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"0B", 0, true},
		{"512B", 512, true},
		{"12.5kB", 12500, true},
		{"428MB", 428_000_000, true},
		{"1.5GB", 1_500_000_000, true},
		{"2GiB", 2 * 1024 * 1024 * 1024, true},
		{"not-a-size", 0, false},
	}
	for _, c := range cases {
		got, ok := parseDockerSize(c.in)
		if ok != c.ok || got != c.want {
			t.Fatalf("parseDockerSize(%q) = (%d, %v), want (%d, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

// With Docker away the containers of removed previews can't be removed, but
// their files still are: the rows are gone, so nothing would come back for
// them. The container's record stays, to be tried again.
func TestCleanupRemovesFilesWithoutDocker(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "pier.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.EnsureProject(ctx, "docs"); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-31 * 24 * time.Hour)
	if err := st.CreateDeployment(ctx, store.Deployment{ID: "aaaaaa", Project: "docs", Target: store.TargetPreview, Status: store.Ready, CreatedAt: old}); err != nil {
		t.Fatal(err)
	}
	if err := st.TrackApp(ctx, "aaaaaa", "pier-docs-aaaaaa"); err != nil {
		t.Fatal(err)
	}
	dep := &deploy.Deployer{Store: st, Deployments: filepath.Join(root, "deployments"), Sources: filepath.Join(root, "sources"), Logs: filepath.Join(root, "logs")}
	files := []string{filepath.Join(dep.Deployments, "aaaaaa", "index.html"), filepath.Join(dep.Sources, "aaaaaa.tar.gz"), filepath.Join(dep.Logs, "aaaaaa.log")}
	for _, f := range files {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte("kept too long"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.QueuePreviewCleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if err := dep.CleanupApps(ctx); err == nil {
		t.Fatal("the app clean-up should fail without a runtime, or this test shows nothing")
	}
	a := &API{Store: st, Deployer: dep, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := a.finishFiles(ctx); err != nil {
		t.Fatalf("finishFiles: %v", err)
	}
	for _, f := range files {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("%s is still there (%v)", f, err)
		}
	}
	if n, err := st.CleanupCount(ctx); n != 0 || err != nil {
		t.Errorf("%d previews still counted for clean-up, %v", n, err)
	}
	if apps, err := st.UnusedApps(ctx); err != nil || len(apps) != 1 {
		t.Errorf("the container should stay recorded for a later try: %v, %v", apps, err)
	}
}

// under is the measurement under way, so a test can wait for its end.
func (m *meter) under() chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

func TestMeterMeasuresOneAtATimeAndKeepsTheLastSize(t *testing.T) {
	ctx := context.Background()
	m := &meter{every: time.Hour, wait: time.Millisecond}
	var started atomic.Int32
	var answer atomic.Pointer[int64]
	release := make(chan struct{})
	measure := func() *int64 {
		started.Add(1)
		<-release
		return answer.Load()
	}
	finish := func(v *int64) {
		t.Helper()
		done := m.under()
		if done == nil {
			t.Fatal("no measurement is running")
		}
		answer.Store(v)
		release <- struct{}{}
		<-done
	}
	size := func(v int64) *int64 { return &v }
	is := func(got *int64, want int64) bool { return got != nil && *got == want }

	// Nothing is known and the measurement is slow: every request gets
	// "unknown" after a moment, and they share the one measurement.
	for i := 0; i < 5; i++ {
		if got := m.size(ctx, measure); got != nil {
			t.Fatalf("a size before any measurement ended: %d", *got)
		}
	}
	finish(size(42))
	if n := started.Load(); n != 1 {
		t.Fatalf("%d measurements started for 5 requests, want 1", n)
	}

	// The answer stands for a while: no measuring on every request.
	for i := 0; i < 5; i++ {
		if got := m.size(ctx, measure); !is(got, 42) {
			t.Fatalf("got %v, want 42", got)
		}
	}
	if n := started.Load(); n != 1 {
		t.Fatalf("%d measurements started while the last one stood, want 1", n)
	}

	// When it's due again the last size is served while the new one runs.
	m.every = 0
	for i := 0; i < 5; i++ {
		if got := m.size(ctx, measure); !is(got, 42) {
			t.Fatalf("got %v while measuring again, want the last size, 42", got)
		}
	}
	m.every = time.Hour
	finish(size(43))
	if got := m.size(ctx, measure); !is(got, 43) || started.Load() != 2 {
		t.Fatalf("got %v from %d measurements, want 43 from 2", got, started.Load())
	}

	// After the cache is cleared the old size is wrong: it's measured again,
	// and a measurement that fails is unknown, not tried on every request.
	m.forget()
	if got := m.size(ctx, measure); got != nil {
		t.Fatalf("a size after forget: %d", *got)
	}
	finish(nil)
	if got := m.size(ctx, measure); got != nil || started.Load() != 3 {
		t.Fatalf("after a failed measurement: size %v, %d started, want unknown and 3", got, started.Load())
	}
}

// Each API measures its own builder's cache: what one measures or forgets
// doesn't reach another.
func TestEachAPIHasItsOwnCacheMeter(t *testing.T) {
	ctx := context.Background()
	var a, b API
	if m := a.cacheSize.get(); m != a.cacheSize.get() || m.every != 5*time.Minute || m.wait != 2*time.Second {
		t.Fatalf("the meter should be made once, measuring every 5 minutes: %+v", m)
	}
	n := int64(42)
	if got := a.cacheSize.get().size(ctx, func() *int64 { return &n }); got == nil || *got != 42 {
		t.Fatalf("got %v, want 42", got)
	}
	if got := b.cacheSize.get().size(ctx, func() *int64 { return nil }); got != nil {
		t.Fatalf("another API's size reached this one: %d", *got)
	}
	b.cacheSize.get().forget()
	again := func() *int64 { t.Error("the size was measured again after another API forgot its own"); return nil }
	if got := a.cacheSize.get().size(ctx, again); got == nil || *got != 42 {
		t.Fatalf("got %v, want the 42 that still stands", got)
	}
}

// fakeDocker puts a docker in PATH that writes down what it was asked and
// then runs script, and returns where the calls are written.
func fakeDocker(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a shell")
	}
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	body := "#!/bin/sh\necho \"$*\" >> '" + calls + "'\n" + script + "\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return calls
}

// The mountpoint is one pierd can't read, as for a service that isn't root.
const fakeVolume = `volume) echo /nonexistent/docker/volumes/pier-build-cache/_data ;;`

func TestMeasureCacheReadsDu(t *testing.T) {
	calls := fakeDocker(t, `case "$1" in `+fakeVolume+` run) printf '2048\t/cache\n' ;; esac`)
	got := measureCache("pier-build-cache", "node:22-slim", 10*time.Second)
	if got == nil || *got != 2048*1024 {
		t.Fatalf("got %v, want 2048 KiB", got)
	}
	asked, _ := os.ReadFile(calls)
	if !strings.Contains(string(asked), "run --name pier-build-cache-size ") || strings.Contains(string(asked), "rm -f") {
		t.Fatalf("docker was asked:\n%s", asked)
	}
}

// A measurement that takes too long is given up, and giving up on the docker
// command doesn't stop the container: it has to be removed by name.
func TestMeasureCacheStopsTheContainerItGaveUpOn(t *testing.T) {
	calls := fakeDocker(t, `case "$1" in `+fakeVolume+` run) exec sleep 30 ;; esac`)
	start := time.Now()
	if got := measureCache("pier-build-cache", "node:22-slim", 300*time.Millisecond); got != nil {
		t.Fatalf("got %d from a measurement that never ended", *got)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("giving up took %v", took)
	}
	asked, _ := os.ReadFile(calls)
	if !strings.Contains(string(asked), "run --name pier-build-cache-size ") || !strings.Contains(string(asked), "rm -f pier-build-cache-size\n") {
		t.Fatalf("docker was asked:\n%s", asked)
	}
}
