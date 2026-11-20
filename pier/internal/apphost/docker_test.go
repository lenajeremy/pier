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
