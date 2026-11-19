package gitssh

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParseCommand(t *testing.T) {
	ok := map[string][2]string{
		"git-receive-pack 'blog'":        {"git-receive-pack", "blog"},
		"git-receive-pack '/blog.git'":   {"git-receive-pack", "blog"},
		"git-upload-pack '/status-page'": {"git-upload-pack", "status-page"},
		`git-receive-pack "docs.git"`:    {"git-receive-pack", "docs"},
	}
	for cmd, want := range ok {
		verb, project, err := ParseCommand(cmd)
		if err != nil || verb != want[0] || project != want[1] {
			t.Errorf("ParseCommand(%q) = %q, %q, %v", cmd, verb, project, err)
		}
	}
	for _, cmd := range []string{"ls -la", "git-receive-pack", "git-receive-pack '../etc'", "git-receive-pack 'Blog'", "rm -rf /", "git-receive-pack 'a/b'"} {
		if _, _, err := ParseCommand(cmd); err == nil {
			t.Errorf("ParseCommand(%q) should fail", cmd)
		}
	}
}

func TestHostKeyIsStable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "host_key")
	a, err := LoadOrCreateHostKey(path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadOrCreateHostKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(a.PublicKey().Marshal()) != string(b.PublicKey().Marshal()) {
		t.Fatal("the host key changed between starts")
	}
}

func TestHookScriptQuotes(t *testing.T) {
	s := HookScript("/opt/it's here/pierd")
	if !strings.Contains(s, `'/opt/it'\''s here/pierd' hook`) {
		t.Fatalf("got %q", s)
	}
}
