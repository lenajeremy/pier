package api

import (
	"strings"
	"testing"
)

func TestParseDotEnv(t *testing.T) {
	raw := strings.Join([]string{
		"# Railway raw variables",
		"",
		"DATABASE_URL=postgres://user:p#ss@host:5432/db?sslmode=require",
		"export API_KEY = \"sk_live_123\"",
		"SINGLE='literal\\nvalue'",
		"ESCAPED=\"line1\\nline2\\t\\\"quoted\\\"\"",
		"PEM=\"-----BEGIN PRIVATE KEY-----",
		"MIIEvQIBADANBgkqhkiG9w0BAQEF",
		"-----END PRIVATE KEY-----\"",
		"DUP=first",
		"DUP=second",
		"EMPTY=",
	}, "\n")

	pairs, err := parseDotEnv(raw)
	if err != nil {
		t.Fatalf("parseDotEnv: %v", err)
	}
	want := []envPair{
		{"DATABASE_URL", "postgres://user:p#ss@host:5432/db?sslmode=require"},
		{"API_KEY", "sk_live_123"},
		{"SINGLE", `literal\nvalue`},
		{"ESCAPED", "line1\nline2\t\"quoted\""},
		{"PEM", "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEF\n-----END PRIVATE KEY-----"},
		{"DUP", "second"},
		{"EMPTY", ""},
	}
	if len(pairs) != len(want) {
		t.Fatalf("got %d pairs (%+v), want %d", len(pairs), pairs, len(want))
	}
	for i := range want {
		if pairs[i] != want[i] {
			t.Fatalf("pair %d: got %+v, want %+v", i, pairs[i], want[i])
		}
	}
}

func TestParseDotEnvErrors(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"missing equals", "JUST_A_NAME"},
		{"invalid key", "123BAD=1"},
		{"reserved key", "PATH=/usr/bin"},
		{"unterminated quote", "FOO=\"hello\nworld"},
		{"trailing junk after quote", "FOO=\"bar\" baz"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := parseDotEnv(c.raw); err == nil {
				t.Fatalf("expected error for %q", c.raw)
			}
		})
	}
}
