package bundle

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// entry is one member of a test archive: a folder when the name ends in a
// slash, a link when it has a target, and otherwise a file.
type entry struct{ name, body, link string }

func archive(t *testing.T, entries ...entry) string {
	t.Helper()
	f, err := os.Create(filepath.Join(t.TempDir(), "source.tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		switch {
		case e.link != "":
			h.Typeflag, h.Linkname, h.Size = tar.TypeSymlink, e.link, 0
		case e.name[len(e.name)-1] == '/':
			h.Typeflag, h.Mode = tar.TypeDir, 0o755
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := errors.Join(tw.Close(), gz.Close(), f.Close()); err != nil {
		t.Fatal(err)
	}
	return f.Name()
}

func TestUntarGitHubKeepsLinksInsideTheRepository(t *testing.T) {
	dir := t.TempDir()
	err := UntarGitHub(archive(t,
		entry{name: "owner-site-abc/"},
		entry{name: "owner-site-abc/README.md", link: "docs/README.md"},
		entry{name: "owner-site-abc/docs/"},
		entry{name: "owner-site-abc/docs/README.md", body: "read me"},
		entry{name: "owner-site-abc/site/pages/guide", link: "../../docs"},
	), dir)
	if err != nil {
		t.Fatal(err)
	}
	for link, want := range map[string]string{"README.md": "docs/README.md", "site/pages/guide": "../../docs"} {
		if got, err := os.Readlink(filepath.Join(dir, link)); err != nil || got != want {
			t.Fatalf("%s links to %q, %v; want %q", link, got, err, want)
		}
	}
	for _, name := range []string{"README.md", "site/pages/guide/README.md"} {
		if body, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(body) != "read me" {
			t.Fatalf("%s reads %q, %v", name, body, err)
		}
	}
}

func TestUntarGitHubRejectsLinksThatLeaveTheRepository(t *testing.T) {
	for name, entries := range map[string][]entry{
		"absolute":      {{name: "repo/passwd", link: "/etc/passwd"}},
		"parent":        {{name: "repo/up", link: ".."}},
		"sibling":       {{name: "repo/docs/out", link: "../../outside"}},
		"through links": {{name: "repo/here", link: "."}, {name: "repo/up", link: "here/.."}},
	} {
		t.Run(name, func(t *testing.T) {
			err := UntarGitHub(archive(t, append([]entry{{name: "repo/"}}, entries...)...), t.TempDir())
			if !errors.Is(err, ErrUnsafe) {
				t.Fatalf("got %v, want ErrUnsafe", err)
			}
		})
	}
}

// A link can read as staying inside and still leave through another link.
// Nothing may be written through it before the final check refuses it.
func TestUntarGitHubNeverWritesThroughALink(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "checkout")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	err := UntarGitHub(archive(t,
		entry{name: "repo/"},
		entry{name: "repo/here", link: "."},
		entry{name: "repo/up", link: "here/.."},
		entry{name: "repo/up/escaped.txt", body: "outside"},
	), dir)
	if err == nil {
		t.Fatal("the archive was unpacked")
	}
	if _, err := os.Lstat(filepath.Join(parent, "escaped.txt")); err == nil {
		t.Fatal("a file was written outside the checkout")
	}
}

func TestUntarStillRejectsEveryLink(t *testing.T) {
	dir := t.TempDir()
	err := Untar(archive(t, entry{name: "index.html", body: "home"}, entry{name: "copy.html", link: "index.html"}), dir)
	if !errors.Is(err, ErrUnsafe) {
		t.Fatalf("got %v, want ErrUnsafe", err)
	}
	if body, err := os.ReadFile(filepath.Join(dir, "index.html")); err != nil || string(body) != "home" {
		t.Fatalf("index.html reads %q, %v", body, err)
	}
}
