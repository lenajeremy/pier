package api

import "testing"

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
