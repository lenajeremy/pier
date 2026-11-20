package main

import "testing"

func TestRecords(t *testing.T) {
	type row struct{ typ, host, val string }
	gotRows := func(rs []record) []row {
		out := make([]row, len(rs))
		for i, r := range rs {
			out[i] = row{r.Type, r.Host, r.Value}
		}
		return out
	}
	cases := []struct {
		name, ip, addr, base string
		want                 []row
	}{
		{
			name: "subdomain dashboard",
			ip:   "198.51.100.10", addr: "pier.example.com", base: "example.com",
			want: []row{
				{"A", "pier", "198.51.100.10"},
				{"A", "*", "198.51.100.10"},
				{"NS", "_acme-challenge", "pier.example.com"},
			},
		},
		{
			name: "apex dashboard delegates NS to pier subdomain",
			ip:   "198.51.100.10", addr: "example.com", base: "example.com",
			want: []row{
				{"A", "@", "198.51.100.10"},
				{"A", "pier", "198.51.100.10"},
				{"A", "*", "198.51.100.10"},
				{"NS", "_acme-challenge", "pier.example.com"},
			},
		},
		{
			name: "ip dashboard with base domain",
			ip:   "198.51.100.10", addr: "198.51.100.10", base: "example.com",
			want: []row{
				{"A", "pier", "198.51.100.10"},
				{"A", "*", "198.51.100.10"},
				{"NS", "_acme-challenge", "pier.example.com"},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := gotRows(records(c.ip, c.addr, c.base))
			if len(got) != len(c.want) {
				t.Fatalf("got %+v, want %+v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("row %d: got %+v, want %+v", i, got[i], c.want[i])
				}
			}
		})
	}
}
