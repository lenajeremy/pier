package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"pier/internal/version"
)

// cliBundle is a CLI release bundle as release-cli.sh makes it. Its pier is
// a script that says its version, so the check that it runs can run it.
func cliBundle(v string, damage bool) []byte {
	top := "pier-cli-" + runtime.GOOS + "-" + runtime.GOARCH
	files := map[string]string{
		"pier": "#!/bin/sh\necho pier " + v + "\n", "OS": runtime.GOOS + "\n", "ARCH": runtime.GOARCH + "\n", "VERSION": v + "\n",
	}
	var sums strings.Builder
	for _, name := range []string{"pier", "OS", "ARCH", "VERSION"} {
		s := sha256.Sum256([]byte(files[name]))
		fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(s[:]), name)
	}
	if damage {
		files["pier"] += "# tampered\n"
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

// fakeReleases serves a latest release and its CLI bundle, and puts a pier
// that says v0.7.0 in a temp folder, as the pier being replaced.
func fakeReleases(t *testing.T, latest string, damage bool) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/latest":
			fmt.Fprintf(w, `{"tag_name":%q}`, latest)
		case r.URL.Path == "/"+latest+"/pier-cli-"+runtime.GOOS+"-"+runtime.GOARCH+".tar.gz":
			w.Write(cliBundle(latest, damage))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	saved := []string{releaseBase, latestAPI, version.Version}
	savedExe := executable
	t.Cleanup(func() {
		releaseBase, latestAPI, version.Version, executable = saved[0], saved[1], saved[2], savedExe
	})
	releaseBase, latestAPI, version.Version = srv.URL, srv.URL+"/latest", "v0.7.0"
	exe := filepath.Join(t.TempDir(), "pier")
	os.WriteFile(exe, []byte("#!/bin/sh\necho pier v0.7.0\n"), 0o755)
	executable = func() (string, error) { return exe, nil }
	return exe
}

func read(t *testing.T, path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestUpgradeReplacesTheRunningPier(t *testing.T) {
	exe := fakeReleases(t, "v0.7.1", false)
	if err := upgradeCLI(); err != nil {
		t.Fatal(err)
	}
	if got := read(t, exe); got != "#!/bin/sh\necho pier v0.7.1\n" {
		t.Fatalf("pier is now %q", got)
	}
	if fi, _ := os.Stat(exe); fi.Mode().Perm() != 0o755 {
		t.Errorf("mode %v", fi.Mode())
	}
	if entries, _ := os.ReadDir(filepath.Dir(exe)); len(entries) != 1 {
		t.Errorf("left beside pier: %v", entries)
	}
}

func TestUpgradeWhenUpToDate(t *testing.T) {
	exe := fakeReleases(t, "v0.7.0", false)
	if err := upgradeCLI(); err != nil {
		t.Fatal(err)
	}
	if read(t, exe) != "#!/bin/sh\necho pier v0.7.0\n" {
		t.Fatal("an up-to-date pier was replaced")
	}
}

func TestUpgradeRefusesADamagedDownload(t *testing.T) {
	exe := fakeReleases(t, "v0.7.1", true)
	err := upgradeCLI()
	if err == nil || !strings.Contains(err.Error(), "doesn't match its checksum") || !strings.Contains(err.Error(), "pier wasn't changed") {
		t.Fatalf("got %v", err)
	}
	if read(t, exe) != "#!/bin/sh\necho pier v0.7.0\n" {
		t.Fatal("pier changed")
	}
}

// In a folder pier can't write to, it says so, and how to do it instead,
// before downloading anything.
func TestUpgradeInAFolderItCantWrite(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	exe := fakeReleases(t, "v0.7.1", false)
	dir := filepath.Dir(exe)
	os.Chmod(dir, 0o555)
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	err := upgradeCLI()
	if err == nil || !strings.Contains(err.Error(), "this pier is in "+dir+", which it can't write to") || !strings.Contains(err.Error(), "sudo "+exe+" upgrade") || !strings.Contains(err.Error(), "cli.sh | sh") {
		t.Fatalf("got %v", err)
	}
	if read(t, exe) != "#!/bin/sh\necho pier v0.7.0\n" {
		t.Fatal("pier changed")
	}
}

func TestUpgradeLeavesABuildFromSource(t *testing.T) {
	exe := fakeReleases(t, "v0.7.1", false)
	version.Version = ""
	if err := upgradeCLI(); err == nil || !strings.Contains(err.Error(), "build from source") {
		t.Fatalf("got %v", err)
	}
	if read(t, exe) != "#!/bin/sh\necho pier v0.7.0\n" {
		t.Fatal("pier changed")
	}
}
