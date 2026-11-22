package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pier/internal/upgrade"
	"pier/internal/version"
)

// The owner asks for an update; pierd writes the version down for the
// helper, and refuses what the helper would refuse.
func TestRequestUpgrade(t *testing.T) {
	saved := version.Version
	version.Version = "v0.7.0"
	t.Cleanup(func() { version.Version = saved })
	tm := newTeam(t, false)
	tm.api.Data = t.TempDir()
	unit := filepath.Join(t.TempDir(), "pierd-upgrade.path")
	tm.api.UpgradeUnit = unit

	// Without the helper, it says how to install it.
	resp, body := tm.send(t, tm.owner, "POST", "/api/server/upgrade", `{"version":"v0.7.1"}`)
	if resp.StatusCode != http.StatusConflict || body["error"] != "Run the installer once more to turn on updates from here." || !strings.Contains(body["command"].(string), "get.sh | sudo sh") {
		t.Fatalf("without the helper: %d %v", resp.StatusCode, body)
	}
	os.WriteFile(unit, []byte("[Path]\n"), 0o644)

	for req, want := range map[string]string{
		`{"version":"latest"}`:         "Choose a release like v0.7.1.",
		`{"version":"v0.7.1; rm -rf"}`: "Choose a release like v0.7.1.",
		`{"version":"v0.7.0"}`:         "v0.7.0 isn't newer than v0.7.0, which is running.",
		`{"version":"v0.6.0"}`:         "v0.6.0 isn't newer than v0.7.0, which is running.",
	} {
		if _, body := tm.send(t, tm.owner, "POST", "/api/server/upgrade", req); body["error"] != want {
			t.Errorf("%s: %v", req, body)
		}
	}
	if upgrade.Pending(tm.api.Data) {
		t.Fatal("a refused request was written")
	}
	if _, body := tm.send(t, tm.member, "POST", "/api/server/upgrade", `{"version":"v0.7.1"}`); body["error"] != "Only the owner can update the server." {
		t.Errorf("a member asking: %v", body)
	}

	if resp, body := tm.send(t, tm.owner, "POST", "/api/server/upgrade", `{"version":"v0.7.1"}`); resp.StatusCode != http.StatusAccepted || body["requested"] != "v0.7.1" {
		t.Fatalf("asking: %d %v", resp.StatusCode, body)
	}
	if b, _ := os.ReadFile(filepath.Join(tm.api.Data, upgrade.Dir, upgrade.RequestFile)); string(b) != "v0.7.1\n" {
		t.Fatalf("the request reads %q", b)
	}
	if _, body := tm.send(t, tm.owner, "POST", "/api/server/upgrade", `{"version":"v0.7.1"}`); body["error"] != "An update is already under way." {
		t.Errorf("asking twice: %v", body)
	}
	_, body = tm.send(t, tm.owner, "GET", "/api/server/upgrade", "")
	if body["pending"] != true || body["helper"] != true || body["running"] != "v0.7.0" {
		t.Errorf("progress: %v", body)
	}
	if _, body := tm.send(t, tm.viewer, "GET", "/api/version", ""); body["upgrade"] != true || body["version"] != "v0.7.0" {
		t.Errorf("version: %v", body)
	}
}
