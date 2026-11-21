package apphost

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"slices"
	"strings"
	"testing"

	"pier/internal/apphost/dockertest"
)

// A stopped deployment keeps its container, stays down across a reboot, and
// can be brought back; nothing of another instance's is touched.
func TestStopAndResume(t *testing.T) {
	ctx := context.Background()
	engine := dockertest.Start(t)
	h := New(t.TempDir())
	engine.Add(dockertest.Container{Name: "pier-app-old", Labels: h.Labels(), Running: true, Policy: "always"})
	newAddr := engine.Add(dockertest.Container{Name: "pier-app-new", Labels: h.Labels(), Running: true, Policy: "unless-stopped"})
	engine.Add(dockertest.Container{Name: "pier-app-bad", Labels: h.Labels(), Policy: "unless-stopped", Crashes: true})
	engine.Add(dockertest.Container{Name: "another-instance", Labels: map[string]string{ownerLabel: "pier-other"}, Running: true})
	calls := func(want ...string) {
		t.Helper()
		if got := engine.Calls(); !slices.Equal(got, want) {
			t.Fatalf("engine calls = %q, want %q", got, want)
		}
	}

	running, err := h.Running(ctx)
	if err != nil || len(running) != 2 || !running["pier-app-old"] || !running["pier-app-new"] {
		t.Fatalf("running = %v, %v", running, err)
	}

	// "always" would start it again when Docker restarts.
	if err := h.Stop(ctx, "pier-app-old"); err != nil {
		t.Fatal(err)
	}
	stopped := []string{"update pier-app-old unless-stopped", "stop pier-app-old"}
	calls(stopped...)
	for _, name := range []string{"pier-app-old", "pier-app-gone"} {
		if err := h.Stop(ctx, name); err != nil {
			t.Fatalf("stopping %s again: %v", name, err)
		}
	}
	calls(stopped...)
	if err := h.Stop(ctx, "another-instance"); err == nil {
		t.Fatal("stopped a container this instance doesn't own")
	}
	endpoints, err := h.Endpoints(ctx)
	if err != nil || len(endpoints) != 1 || endpoints["pier-app-new"] != newAddr {
		t.Fatalf("endpoints = %v, %v", endpoints, err)
	}

	for _, name := range []string{"pier-app-old", "pier-app-new"} {
		if err := h.Resume(ctx, name); err != nil {
			t.Fatalf("resuming %s: %v", name, err)
		}
	}
	calls(append(stopped, "start pier-app-old")...)
	if got := engine.Running(); !slices.Equal(got, []string{"another-instance", "pier-app-new", "pier-app-old"}) {
		t.Fatalf("running after resume = %v", got)
	}

	if err := h.Resume(ctx, "pier-app-bad"); err == nil || !strings.Contains(err.Error(), "exit code 23") {
		t.Fatalf("a crashing process resumed: %v", err)
	}
	if err := h.Resume(ctx, "pier-app-gone"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("resuming a removed container: %v", err)
	}
	if err := h.Resume(ctx, "another-instance"); err == nil {
		t.Fatal("resumed a container this instance doesn't own")
	}
}

func TestHTTPPort(t *testing.T) {
	for _, tc := range []struct {
		name    string
		vars    []string
		exposed map[string]struct{}
		want    string
	}{
		{"default", nil, nil, "8080"},
		{"image", nil, map[string]struct{}{"3000/tcp": {}, "53/udp": {}}, "3000"},
		{"override", []string{"PORT=5000"}, map[string]struct{}{"3000/tcp": {}, "9000/tcp": {}}, "5000"},
		{"ambiguous", nil, map[string]struct{}{"3000/tcp": {}, "9000/tcp": {}}, ""},
		{"invalid", []string{"PORT=0"}, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := appPort(tc.vars, tc.exposed)
			if tc.want == "" {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil || p.Port() != tc.want {
				t.Fatalf("got %v, %v", p, err)
			}
		})
	}
}

func TestParseContainerLogs(t *testing.T) {
	frame := func(stream byte, body string) []byte {
		var buf bytes.Buffer
		buf.Write([]byte{stream, 0, 0, 0})
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(body)))
		buf.Write(n[:])
		buf.WriteString(body)
		return buf.Bytes()
	}
	var raw []byte
	raw = append(raw, frame(1, "2026-11-20T15:00:00.123Z listening on :8080\n")...)
	raw = append(raw, frame(2, "2026-11-20T15:00:01.000Z warning: slow query\r\n")...)
	lines := parseContainerLogs(raw, false)
	if len(lines) != 2 {
		t.Fatalf("got %d lines: %+v", len(lines), lines)
	}
	if lines[0].Stream != "stdout" || lines[0].At != "2026-11-20T15:00:00.123Z" || lines[0].Text != "listening on :8080" {
		t.Fatalf("line 0 = %+v", lines[0])
	}
	if lines[1].Stream != "stderr" || lines[1].Text != "warning: slow query" {
		t.Fatalf("line 1 = %+v", lines[1])
	}
}

func TestResolveResourcesAndHealthPath(t *testing.T) {
	def := resolveResources(StartOptions{})
	if def.Memory != DefaultMemoryMB*1024*1024 || def.NanoCPUs != 0 {
		t.Fatalf("default resources = %+v", def)
	}
	unlimited := resolveResources(StartOptions{MemoryMB: -1})
	if unlimited.Memory != 0 {
		t.Fatalf("unlimited memory = %d, want 0", unlimited.Memory)
	}
	custom := resolveResources(StartOptions{MemoryMB: 512, CPUs: 1.5})
	if custom.Memory != 512*1024*1024 || custom.NanoCPUs != 1_500_000_000 {
		t.Fatalf("custom resources = %+v", custom)
	}
	env := withHealthPath([]string{"PORT=3000", "PIER_HEALTH_PATH=/old"}, "/api/health")
	if got := envValue(env, "PIER_HEALTH_PATH"); got != "/api/health" {
		t.Fatalf("health path = %q, want /api/health", got)
	}
	if len(env) != 2 {
		t.Fatalf("expected deduplicated env, got %v", env)
	}
}

func TestFollowSplitDockerFrames(t *testing.T) {
	var raw bytes.Buffer
	frame := func(stream byte, s string) {
		raw.Write([]byte{stream, 0, 0, 0})
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(s)))
		raw.Write(n[:])
		raw.WriteString(s)
	}
	frame(1, "2026-11-20T15:00:01Z hel")
	frame(2, "warning\n")
	frame(1, "lo\nlast")
	var lines []LogLine
	if err := FollowLogLines(&raw, false, func(l LogLine) error { lines = append(lines, l); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(lines) != 3 || lines[0].Stream != "stderr" || lines[1].At == "" || lines[1].Text != "hello" || lines[2].Text != "last" {
		t.Fatalf("unexpected output %+v", lines)
	}
}
