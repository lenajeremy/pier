package apphost

import (
	"bytes"
	"encoding/binary"
	"testing"
)

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
