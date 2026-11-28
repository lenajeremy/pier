package api

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"time"

	"pier/internal/release"
	"pier/internal/upgrade"
	"pier/internal/version"
)

// installCommand is the installer, run again on the server to install the
// update helper on a server set up before updates from the dashboard.
const installCommand = "curl -fsSL https://raw.githubusercontent.com/lenajeremy/pier/main/pier/install/get.sh | sudo sh"

// helperInstalled says whether the root helper that applies an update is
// installed: pierd can't apply one itself.
func (a *API) helperInstalled() bool {
	if a.UpgradeUnit == "" {
		return false
	}
	_, err := os.Stat(a.UpgradeUnit)
	return err == nil
}

// upgradeInfo is what the Server page shows the owner about updates: the
// release running, the newest one, whether this server can update itself,
// and how the last update went.
func (a *API) upgradeInfo(w http.ResponseWriter, r *http.Request) {
	running, latest := version.String(), a.Releases.Fresh(r.Context())
	out := map[string]any{
		"running": running, "latest": latest, "newer": release.Newer(latest, running),
		"helper": a.helperInstalled(), "command": installCommand,
		"pending": a.Data != "" && upgrade.Pending(a.Data), "status": nil,
	}
	if latest != "" {
		out["notes"] = release.Notes(latest)
	}
	if a.Data != "" {
		if st, ok := upgrade.ReadStatus(a.Data); ok {
			out["status"] = st
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// requestUpgrade asks the helper to update pierd to a release. pierd only
// writes the version down; the helper, running as root, checks it again,
// downloads that release from GitHub and restarts pierd.
func (a *API) requestUpgrade(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Version string `json:"version"`
	}
	json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req)
	running := version.String()
	switch {
	case !a.helperInstalled() || a.Data == "":
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Run the installer once more to turn on updates from here.", "command": installCommand})
		return
	case !release.Valid(req.Version):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Choose a release like v0.7.1."})
		return
	case !release.Valid(running):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "This pierd is a build from source (" + running + "), so it isn't updated from releases."})
		return
	case !release.Newer(req.Version, running):
		writeJSON(w, http.StatusConflict, map[string]string{"error": req.Version + " isn't newer than " + running + ", which is running."})
		return
	}
	if st, ok := upgrade.ReadStatus(a.Data); upgrade.Pending(a.Data) || ok && st.State == upgrade.Started && time.Since(st.At) < 15*time.Minute {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "An update is already under way."})
		return
	}
	if err := upgrade.Request(a.Data, req.Version); err != nil {
		a.fail(w, err)
		return
	}
	a.event(r.Context(), "", "Asked to update pierd", "To "+req.Version, "")
	a.Log.Info("update requested", "version", req.Version)
	writeJSON(w, http.StatusAccepted, map[string]string{"requested": req.Version})
}
