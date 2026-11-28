package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVersions(t *testing.T) {
	for v, ok := range map[string]bool{
		"v0.7.0": true, "v10.20.300": true, "v0.0.1": true,
		"0.7.0": false, "v0.7": false, "v0.7.0-rc1": false, "v0.07.0": false, "v0.7.0\n": false,
		"latest": false, "": false, "dev-3f9a2c1": false, "v0.7.0 ": false, "v1.2.3.4": false,
	} {
		if Valid(v) != ok {
			t.Errorf("Valid(%q) = %v", v, !ok)
		}
	}
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"v0.7.1", "v0.7.0", true}, {"v0.10.0", "v0.9.9", true}, {"v1.0.0", "v0.99.99", true},
		{"v0.7.0", "v0.7.0", false}, {"v0.6.9", "v0.7.0", false}, {"v0.7.1", "dev-3f9a2c1", false},
	} {
		if Newer(tc.a, tc.b) != tc.want {
			t.Errorf("Newer(%s, %s) = %v", tc.a, tc.b, !tc.want)
		}
	}
}

func tarball(t *testing.T, entries ...tar.Header) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, h := range entries {
		body := strings.Repeat("x", int(h.Size))
		tw.WriteHeader(&h)
		tw.Write([]byte(body))
	}
	tw.Close()
	zw.Close()
	return buf.Bytes()
}

// A bundle is one folder of plain files; anything else is refused.
func TestFetchRefusesWhatIsntAPlainFile(t *testing.T) {
	for name, entry := range map[string]tar.Header{
		"climbs out":     {Name: "pier-cli-darwin-arm64/../../evil", Typeflag: tar.TypeReg, Size: 1},
		"deeper":         {Name: "pier-cli-darwin-arm64/sub/pier", Typeflag: tar.TypeReg, Size: 1},
		"a link":         {Name: "pier-cli-darwin-arm64/pier", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
		"another folder": {Name: "elsewhere/pier", Typeflag: tar.TypeReg, Size: 1},
		"absolute":       {Name: "/pier-cli-darwin-arm64/pier", Typeflag: tar.TypeReg, Size: 1},
	} {
		body := tarball(t, tar.Header{Name: "pier-cli-darwin-arm64/", Typeflag: tar.TypeDir}, entry)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(body) }))
		_, err := Fetch(context.Background(), Client(0), srv.URL, "pier-cli-darwin-arm64", t.TempDir())
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), "isn't a plain file") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestVerify(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "pier"), []byte("pier v0.7.1"), 0o644)
	os.WriteFile(filepath.Join(dir, "VERSION"), []byte("v0.7.1\n"), 0o644)
	sums := func(s string) { os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(s), 0o644) }
	const pierSum = "5d84e5b7e9a4b5b4ed1d5e2a8b6c3a1ca1c9a7e1d4d1e1b1c1a1f1e1d1c1b1a1" // not pier's
	sums(sumOf(t, dir, "pier") + "  pier\n" + sumOf(t, dir, "VERSION") + " *VERSION\n")
	if listed, err := Verify(dir, "pier", "VERSION"); err != nil || !listed["pier"] || !listed["VERSION"] {
		t.Fatalf("a good bundle: %v %v", listed, err)
	}
	if _, err := Verify(dir, "pier", "pierd"); err == nil || !strings.Contains(err.Error(), "doesn't list pierd") {
		t.Errorf("a required file missing from the sums: %v", err)
	}
	sums(pierSum + "  pier\n")
	if _, err := Verify(dir, "pier"); err == nil || !strings.Contains(err.Error(), "doesn't match its checksum") {
		t.Errorf("a wrong sum: %v", err)
	}
	sums(sumOf(t, dir, "pier") + "  ../pier\n")
	if _, err := Verify(dir); err == nil {
		t.Error("a sum for a file outside the bundle was accepted")
	}
	os.Remove(filepath.Join(dir, "SHA256SUMS"))
	if _, err := Verify(dir); err == nil {
		t.Error("a bundle without sums was accepted")
	}
}

func sumOf(t *testing.T, dir, name string) string {
	s, err := fileSum(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Replace leaves dst whole, the old one or the new one, and nothing beside
// it.
func TestReplace(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(t.TempDir(), "pier"), filepath.Join(dir, "pier")
	os.WriteFile(src, []byte("new"), 0o600)
	os.WriteFile(dst, []byte("old"), 0o755)
	if err := Replace(src, dst, 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(dst)
	fi, _ := os.Stat(dst)
	entries, _ := os.ReadDir(dir)
	if string(b) != "new" || fi.Mode().Perm() != 0o755 || len(entries) != 1 {
		t.Fatalf("got %q, %v, %d files", b, fi.Mode(), len(entries))
	}
}

func TestFreshAsksAgainOnlyWhenTheAnswerIsOld(t *testing.T) {
	tag, asked := "v0.8.0", 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked++
		fmt.Fprintf(w, `{"tag_name":%q}`, tag)
	}))
	defer srv.Close()
	c := &Checker{API: srv.URL}
	if got := c.Fresh(context.Background()); got != "v0.8.0" || asked != 1 {
		t.Fatalf("first look: %q after %d questions", got, asked)
	}
	tag = "v0.8.1"
	if got := c.Fresh(context.Background()); got != "v0.8.0" || asked != 1 {
		t.Fatalf("a fresh answer was asked again: %q after %d", got, asked)
	}
	c.mu.Lock()
	c.checked = time.Now().Add(-StaleAfter - time.Second)
	c.mu.Unlock()
	if got := c.Fresh(context.Background()); got != "v0.8.1" || asked != 2 {
		t.Fatalf("an old answer wasn't asked again: %q after %d", got, asked)
	}
}
