package upgrade

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// bundle is a release bundle as release.sh makes it, with SHA256SUMS over
// every file but itself. damage changes a file after its sum is taken.
func bundle(t *testing.T, top string, files map[string]string, damage string) []byte {
	t.Helper()
	var sums strings.Builder
	for _, name := range []string{"pier", "pierd", "ARCH", "VERSION", "pierd.service", "pierd-upgrade.path", "pierd-upgrade.service"} {
		if body, ok := files[name]; ok {
			s := sha256.Sum256([]byte(body))
			fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(s[:]), name)
		}
	}
	if damage != "" {
		files[damage] += "tampered"
	}
	files["SHA256SUMS"] = sums.String()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	tw.WriteHeader(&tar.Header{Name: top + "/", Typeflag: tar.TypeDir, Mode: 0o755})
	for name, body := range files {
		tw.WriteHeader(&tar.Header{Name: top + "/" + name, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(body))})
		tw.Write([]byte(body))
	}
	tw.Close()
	zw.Close()
	return buf.Bytes()
}

func releaseFiles(v, arch string) map[string]string {
	return map[string]string{
		"pier": "pier " + v, "pierd": "pierd " + v, "ARCH": arch + "\n", "VERSION": v + "\n",
		"pierd.service":      "[Service]\nExecStart=/usr/local/bin/pierd serve # " + v + "\n",
		"pierd-upgrade.path": "[Path]\n", "pierd-upgrade.service": "[Service]\n",
	}
}

// server is one machine: its data folder, binaries and unit files, and a
// release host that serves bundles and counts what it was asked.
type server struct {
	data, bin, units string
	asked            atomic.Int32
	bundles          map[string][]byte // by path
	host             *httptest.Server
	systemctl        []string
}

func newServer(t *testing.T) *server {
	t.Helper()
	s := &server{data: t.TempDir(), bin: t.TempDir(), units: t.TempDir(), bundles: map[string][]byte{}}
	os.MkdirAll(filepath.Join(s.data, Dir), 0o700)
	os.WriteFile(filepath.Join(s.bin, "pierd"), []byte("pierd v0.7.0"), 0o755)
	os.WriteFile(filepath.Join(s.bin, "pier"), []byte("pier v0.7.0"), 0o755)
	os.WriteFile(filepath.Join(s.units, "pierd.service"), []byte("[Service]\nExecStart=old\n"), 0o644)
	os.WriteFile(filepath.Join(s.units, "pierd-upgrade.path"), []byte("[Path]\n"), 0o644)
	s.host = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.asked.Add(1)
		if b, ok := s.bundles[r.URL.Path]; ok {
			w.Write(b)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(s.host.Close)
	return s
}

func (s *server) ask(t *testing.T, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(s.data, Dir, RequestFile), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (s *server) apply(current string) error {
	return Apply{
		Data: s.data, Bin: s.bin, Units: s.units, Base: s.host.URL, Arch: "amd64", Current: current,
		Systemctl: func(args ...string) error { s.systemctl = append(s.systemctl, strings.Join(args, " ")); return nil },
		// The fake pierd is a file that says its version; running it is
		// reading it.
		Check: func(_ context.Context, path string) (string, error) {
			b, err := os.ReadFile(path)
			return string(b), err
		},
	}.Run(context.Background())
}

func (s *server) installed(t *testing.T, name string) string {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(s.bin, name))
	return string(b)
}

func (s *server) untouched(t *testing.T) {
	t.Helper()
	if s.installed(t, "pierd") != "pierd v0.7.0" || s.installed(t, "pier") != "pier v0.7.0" {
		t.Errorf("the installed binaries changed: %q, %q", s.installed(t, "pierd"), s.installed(t, "pier"))
	}
	if len(s.systemctl) != 0 {
		t.Errorf("systemctl ran: %v", s.systemctl)
	}
	entries, _ := os.ReadDir(s.bin)
	if len(entries) != 2 {
		t.Errorf("the binary folder has %d files, temp files left behind", len(entries))
	}
}

func TestApplyInstallsANewerRelease(t *testing.T) {
	s := newServer(t)
	s.bundles["/v0.7.1/pier-linux-amd64.tar.gz"] = bundle(t, "pier-linux-amd64", releaseFiles("v0.7.1", "amd64"), "")
	s.ask(t, "v0.7.1\n")
	if err := s.apply("v0.7.0"); err != nil {
		t.Fatal(err)
	}
	if s.installed(t, "pierd") != "pierd v0.7.1" || s.installed(t, "pier") != "pier v0.7.1" {
		t.Fatalf("installed %q and %q", s.installed(t, "pierd"), s.installed(t, "pier"))
	}
	if fi, _ := os.Stat(filepath.Join(s.bin, "pierd")); fi.Mode().Perm() != 0o755 {
		t.Errorf("pierd's mode is %v", fi.Mode())
	}
	unit, _ := os.ReadFile(filepath.Join(s.units, "pierd.service"))
	added, _ := os.ReadFile(filepath.Join(s.units, "pierd-upgrade.service"))
	if !strings.Contains(string(unit), "v0.7.1") || string(added) != "[Service]\n" {
		t.Errorf("unit files: %q, %q", unit, added)
	}
	if strings.Join(s.systemctl, ", ") != "daemon-reload, restart pierd" {
		t.Errorf("systemctl ran %v", s.systemctl)
	}
	st, ok := ReadStatus(s.data)
	if !ok || st.State != Done || st.Version != "v0.7.1" {
		t.Errorf("status %+v", st)
	}
	if Pending(s.data) {
		t.Error("the request is still waiting")
	}
	if entries, _ := os.ReadDir(s.bin); len(entries) != 2 {
		t.Errorf("temp files were left in the binary folder: %v", entries)
	}
}

// With unit files that haven't changed, systemd isn't reloaded.
func TestApplyLeavesUnchangedUnits(t *testing.T) {
	s := newServer(t)
	files := releaseFiles("v0.7.1", "amd64")
	os.WriteFile(filepath.Join(s.units, "pierd.service"), []byte(files["pierd.service"]), 0o644)
	os.WriteFile(filepath.Join(s.units, "pierd-upgrade.service"), []byte(files["pierd-upgrade.service"]), 0o644)
	s.bundles["/v0.7.1/pier-linux-amd64.tar.gz"] = bundle(t, "pier-linux-amd64", files, "")
	s.ask(t, "v0.7.1")
	if err := s.apply("v0.7.0"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(s.systemctl, ", ") != "restart pierd" {
		t.Errorf("systemctl ran %v", s.systemctl)
	}
}

// A request is a version and nothing else. Anything that isn't a newer
// release is refused before anything is downloaded, and never repeated in
// the status.
func TestApplyRefusesWhatIsntANewerRelease(t *testing.T) {
	for _, tc := range []struct{ request, current, want string }{
		{"v0.7.0", "v0.7.0", "v0.7.0 isn't newer than v0.7.0"},
		{"v0.6.9", "v0.7.0", "v0.6.9 isn't newer than v0.7.0"},
		{"v0.7.1", "dev-3f9a2c1", "build from source"},
		{"latest", "v0.7.0", "didn't name a release"},
		{"v0.7.1-rc1", "v0.7.0", "didn't name a release"},
		{"v0.7.01", "v0.7.0", "didn't name a release"},
		{"v0.7.1\nhttps://evil.example/x", "v0.7.0", "didn't name a release"},
		{"https://evil.example/pier.tar.gz", "v0.7.0", "didn't name a release"},
		{strings.Repeat("9", 500), "v0.7.0", "didn't name a release"},
	} {
		s := newServer(t)
		s.ask(t, tc.request)
		err := s.apply(tc.current)
		st, _ := ReadStatus(s.data)
		if err == nil || !strings.Contains(err.Error(), tc.want) || st.State != Failed || st.Error != err.Error() {
			t.Errorf("%q on %s: %v, status %+v", tc.request, tc.current, err, st)
		}
		if strings.Contains(st.Error, "evil") || strings.Contains(st.Error, "999") {
			t.Errorf("%q: the status repeats the request: %q", tc.request, st.Error)
		}
		if s.asked.Load() != 0 {
			t.Errorf("%q: the release host was asked", tc.request)
		}
		if Pending(s.data) {
			t.Errorf("%q: the request is still waiting", tc.request)
		}
		s.untouched(t)
	}
}

// A bundle that doesn't match its checksums, or is for another machine or
// release, changes nothing.
func TestApplyRefusesABadBundle(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		top   string
		harm  string
		want  string
	}{
		{"damaged binary", releaseFiles("v0.7.1", "amd64"), "pier-linux-amd64", "pierd", "The bundle's pierd doesn't match its checksum, so the download is damaged. Nothing changed."},
		{"another machine", releaseFiles("v0.7.1", "arm64"), "pier-linux-amd64", "", "for arm64, not this server's amd64"},
		{"another release", releaseFiles("v0.7.2", "amd64"), "pier-linux-amd64", "", "says it's v0.7.2, not v0.7.1"},
		{"wrong folder", releaseFiles("v0.7.1", "amd64"), "elsewhere", "", "isn't a plain file in pier-linux-amd64"},
	} {
		s := newServer(t)
		s.bundles["/v0.7.1/pier-linux-amd64.tar.gz"] = bundle(t, tc.top, tc.files, tc.harm)
		s.ask(t, "v0.7.1")
		err := s.apply("v0.7.0")
		st, _ := ReadStatus(s.data)
		if err == nil || !strings.Contains(err.Error(), tc.want) || st.State != Failed {
			t.Errorf("%s: %v, status %+v", tc.name, err, st)
		}
		s.untouched(t)
	}
	// A release that isn't there.
	s := newServer(t)
	s.ask(t, "v0.9.0")
	if err := s.apply("v0.7.0"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("a missing release: %v", err)
	}
	s.untouched(t)
}

// The new pierd must run here before it's put in place.
func TestApplyRefusesAPierdThatDoesntRun(t *testing.T) {
	s := newServer(t)
	files := releaseFiles("v0.7.1", "amd64")
	files["pierd"] = "exec format error"
	s.bundles["/v0.7.1/pier-linux-amd64.tar.gz"] = bundle(t, "pier-linux-amd64", files, "")
	s.ask(t, "v0.7.1")
	if err := s.apply("v0.7.0"); err == nil || !strings.Contains(err.Error(), "didn't run on this server") {
		t.Fatalf("got %v", err)
	}
	s.untouched(t)
}

// pier owns the folder, so it could put links where the request and the
// status should be. root reads no link, and writes through none.
func TestApplyFollowsNoLinks(t *testing.T) {
	s := newServer(t)
	secret := filepath.Join(t.TempDir(), "shadow")
	os.WriteFile(secret, []byte("v0.7.1"), 0o600)
	victim := filepath.Join(t.TempDir(), "passwd")
	os.WriteFile(victim, []byte("root:x:0:0"), 0o644)
	os.Symlink(secret, filepath.Join(s.data, Dir, RequestFile))
	os.Symlink(victim, filepath.Join(s.data, Dir, StatusFile))
	err := s.apply("v0.7.0")
	if err == nil || !strings.Contains(err.Error(), "didn't name a release") {
		t.Fatalf("a linked request: %v", err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "root:x:0:0" {
		t.Fatalf("the status was written through a link: %q", b)
	}
	if fi, err := os.Lstat(filepath.Join(s.data, Dir, StatusFile)); err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("the status isn't a plain file: %v %v", fi, err)
	}
	if _, err := os.Lstat(filepath.Join(s.data, Dir, RequestFile)); err == nil {
		t.Fatal("the linked request is still there")
	}
	s.untouched(t)

	// Nor is the folder itself followed if it's a link.
	s2 := newServer(t)
	os.RemoveAll(filepath.Join(s2.data, Dir))
	elsewhere := t.TempDir()
	os.WriteFile(filepath.Join(elsewhere, RequestFile), []byte("v0.7.1"), 0o600)
	os.Symlink(elsewhere, filepath.Join(s2.data, Dir))
	if err := s2.apply("v0.7.0"); err == nil {
		t.Fatal("a linked upgrade folder was used")
	}
	if _, err := os.Stat(filepath.Join(elsewhere, StatusFile)); err == nil {
		t.Fatal("a status was written into the linked folder")
	}
}

// Without a request there's nothing to do, and nothing is written.
func TestApplyWithoutARequest(t *testing.T) {
	s := newServer(t)
	if err := s.apply("v0.7.0"); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadStatus(s.data); ok {
		t.Fatal("a status was written")
	}
	s.untouched(t)
}

// pierd's side: a request replaces the last status, and is written whole.
func TestRequest(t *testing.T) {
	data := t.TempDir()
	if err := Request(data, "latest"); err == nil {
		t.Fatal("asked for something that isn't a release")
	}
	os.MkdirAll(filepath.Join(data, Dir), 0o700)
	os.WriteFile(filepath.Join(data, Dir, StatusFile), []byte(`{"state":"done","version":"v0.7.0"}`), 0o644)
	if err := Request(data, "v0.7.1"); err != nil {
		t.Fatal(err)
	}
	if !Pending(data) {
		t.Fatal("no request waiting")
	}
	if _, ok := ReadStatus(data); ok {
		t.Fatal("the last update's status is still there")
	}
	b, _ := os.ReadFile(filepath.Join(data, Dir, RequestFile))
	if string(b) != "v0.7.1\n" {
		t.Fatalf("request %q", b)
	}
	if entries, _ := os.ReadDir(filepath.Join(data, Dir)); len(entries) != 1 {
		t.Fatalf("left behind %v", entries)
	}
}

// The new pierd is really run, from where it was unpacked, before it's put
// in place: one that answers with its version goes in.
func TestApplyRunsTheNewPierd(t *testing.T) {
	s := newServer(t)
	files := releaseFiles("v0.7.1", "amd64")
	files["pierd"] = "#!/bin/sh\necho pierd v0.7.1\n"
	s.bundles["/v0.7.1/pier-linux-amd64.tar.gz"] = bundle(t, "pier-linux-amd64", files, "")
	s.ask(t, "v0.7.1")
	err := Apply{Data: s.data, Bin: s.bin, Units: s.units, Base: s.host.URL, Arch: "amd64", Current: "v0.7.0"}.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.installed(t, "pierd") != files["pierd"] {
		t.Fatalf("installed %q", s.installed(t, "pierd"))
	}
}
