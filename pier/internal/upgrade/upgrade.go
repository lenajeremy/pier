// Package upgrade is how pierd updates itself. pierd runs as the
// unprivileged user pier and can't replace its own binary, which root owns,
// or restart itself, so it only asks: it writes the release it wants into a
// request file in its own data folder. systemd watches for that file
// (pierd-upgrade.path) and starts a root helper for it
// (pierd-upgrade.service), which runs pierd upgrade-apply: the Apply here.
// The helper takes nothing from the request but a version, checks it, and
// downloads that release from a fixed address. It reports how it went in a
// status file pierd reads.
package upgrade

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"pier/internal/release"
)

// Where the request and the status live, under pierd's data folder.
const (
	Dir         = "upgrade"
	RequestFile = "request"
	StatusFile  = "status"
)

// HelperUnit is the systemd unit that starts the helper. If it isn't
// installed, the server was set up by an installer from before updates
// from the dashboard, and the installer has to run once more.
const HelperUnit = "/etc/systemd/system/pierd-upgrade.path"

// The helper's states.
const (
	Started = "started"
	Failed  = "failed"
	Done    = "done"
)

// Status is how the last update went, as the helper wrote it.
type Status struct {
	State   string    `json:"state"`
	Version string    `json:"version,omitempty"`
	Step    string    `json:"step,omitempty"`  // what it's doing, while started
	Error   string    `json:"error,omitempty"` // why it failed
	At      time.Time `json:"at"`
}

// Request asks for an update to version, from pierd's side: it replaces
// any request still waiting, clears the last update's status, and writes
// the request whole, so the helper never reads half of it.
func Request(data, version string) error {
	if !release.Valid(version) {
		return fmt.Errorf("%q isn't a release", version)
	}
	dir := filepath.Join(data, Dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(dir, StatusFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".request.")
	if err != nil {
		return err
	}
	_, err = io.WriteString(tmp, version+"\n")
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), filepath.Join(dir, RequestFile))
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

// Pending reports whether a request is waiting for the helper.
func Pending(data string) bool {
	_, err := os.Lstat(filepath.Join(data, Dir, RequestFile))
	return err == nil
}

// ReadStatus reads the last update's status, if there is one.
func ReadStatus(data string) (Status, bool) {
	var s Status
	b, err := os.ReadFile(filepath.Join(data, Dir, StatusFile))
	if err != nil || json.Unmarshal(b, &s) != nil {
		return Status{}, false
	}
	return s, true
}

// Apply is the root helper: pierd upgrade-apply.
type Apply struct {
	Data    string // pierd's data folder, which holds upgrade/
	Bin     string // where pier and pierd are installed: /usr/local/bin
	Units   string // where the unit files are: /etc/systemd/system
	Base    string // release.DownloadBase, unless root's unit file says otherwise
	Arch    string // this machine's: amd64 or arm64
	Current string // the release that's installed now
	Client  *http.Client
	// Systemctl runs systemctl; nil skips daemon-reload and the restart,
	// as when trying the rest in a container.
	Systemctl func(args ...string) error
	// Check runs the new pierd to make sure it works here before it's put
	// in place; nil runs it with exec.
	Check func(ctx context.Context, path string) (string, error)
	Log   io.Writer
}

// The unit files a bundle may bring, refreshed when they differ.
var units = []string{"pierd.service", "pierd-upgrade.path", "pierd-upgrade.service"}

// Run handles the request waiting, if there is one. Every outcome, the
// refusals included, ends up in the status file; the error it returns is
// the same one. Nothing installed changes until the bundle is downloaded,
// checked and has run here, so a failure before then leaves the running
// pierd as it is.
func (a Apply) Run(ctx context.Context) error {
	dir := filepath.Join(a.Data, Dir)
	// pier owns the data folder and could put a link where the folder
	// should be; root only works in the real folder, and only on names in
	// it (os.Root keeps every open and rename inside).
	if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() {
		return errors.New("there's no upgrade folder to read a request from")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()

	requested, ok := readRequest(root)
	if !ok {
		return nil // nothing asked; the path unit can fire twice
	}
	st := statusWriter{root: root, log: a.Log}
	fail := func(version, format string, args ...any) error {
		err := fmt.Errorf(format, args...)
		st.write(Status{State: Failed, Version: version, Error: err.Error()})
		return err
	}
	// The request is read for one thing, a version, and never repeated
	// back unless it is one.
	if !release.Valid(requested) {
		return fail("", "The request didn't name a release like v1.2.3, so nothing changed.")
	}
	v := requested
	switch {
	case !release.Valid(a.Current):
		return fail(v, "This pierd is a build from source (%s), so it can't tell which release is newer. Install releases with the installer instead.", a.Current)
	case !release.Newer(v, a.Current):
		return fail(v, "%s isn't newer than %s, which is installed, so nothing changed.", v, a.Current)
	}

	base := a.Base
	if base == "" {
		base = release.DownloadBase
	}
	bundle := "pier-linux-" + a.Arch
	url := strings.TrimRight(base, "/") + "/" + v + "/" + bundle + ".tar.gz"
	st.write(Status{State: Started, Version: v, Step: "Downloading " + v})
	client := a.Client
	if client == nil {
		client = release.Client(10 * time.Minute)
	}
	work, err := os.MkdirTemp("", "pier-upgrade-")
	if err != nil {
		return fail(v, "Couldn't make a folder to download into: %v", err)
	}
	defer os.RemoveAll(work)
	src, err := release.Fetch(ctx, client, url, bundle, work)
	if err != nil {
		return fail(v, "%s Nothing changed.", sentence(err))
	}

	st.write(Status{State: Started, Version: v, Step: "Checking " + v})
	listed, err := release.Verify(src, "pier", "pierd", "ARCH", "VERSION")
	if err != nil {
		return fail(v, "%s Nothing changed.", sentence(err))
	}
	if got := release.ReadValue(src, "ARCH"); got != a.Arch {
		return fail(v, "The bundle is for %s, not this server's %s, so nothing changed.", got, a.Arch)
	}
	if got := release.ReadValue(src, "VERSION"); got != v {
		return fail(v, "The bundle says it's %s, not %s, so nothing changed.", got, v)
	}
	check := a.Check
	if check == nil {
		check = runVersion
	}
	if out, err := check(ctx, filepath.Join(src, "pierd")); err != nil || strings.TrimSpace(out) != "pierd "+v {
		return fail(v, "The new pierd didn't run on this server (it said %q), so nothing changed.", strings.TrimSpace(out))
	}

	st.write(Status{State: Started, Version: v, Step: "Installing " + v})
	// Both binaries are copied beside the old ones first; only then is each
	// renamed over its old one, the step that can't leave half a file.
	staged := map[string]string{}
	for _, name := range []string{"pierd", "pier"} {
		tmp, err := release.Stage(filepath.Join(src, name), filepath.Join(a.Bin, name), 0o755)
		if err != nil {
			for _, t := range staged {
				os.Remove(t)
			}
			return fail(v, "Couldn't copy the new %s into %s: %v. Nothing changed.", name, a.Bin, err)
		}
		staged[name] = tmp
	}
	for _, name := range []string{"pierd", "pier"} {
		if err := os.Rename(staged[name], filepath.Join(a.Bin, name)); err != nil {
			for _, t := range staged {
				os.Remove(t)
			}
			return fail(v, "Couldn't put the new %s in place: %v", name, err)
		}
	}
	changed := false
	for _, name := range units {
		have := filepath.Join(a.Units, name)
		if !listed[name] || release.Same(filepath.Join(src, name), have) {
			continue
		}
		if err := release.Replace(filepath.Join(src, name), have, 0o644); err != nil {
			return fail(v, "The new pierd is installed, but its %s couldn't be updated: %v", name, err)
		}
		changed = true
	}

	if a.Systemctl != nil {
		st.write(Status{State: Started, Version: v, Step: "Restarting pierd"})
		if changed {
			if err := a.Systemctl("daemon-reload"); err != nil {
				return fail(v, "The new pierd is installed, but systemd couldn't reload its units: %v", err)
			}
		}
		if err := a.Systemctl("restart", "pierd"); err != nil {
			return fail(v, "The new pierd is installed, but restarting it failed: %v", err)
		}
	}
	st.write(Status{State: Done, Version: v})
	return nil
}

// readRequest reads the version asked for and removes the request, so it's
// handled once. A request is a single short line; anything else isn't one.
// Anything there that isn't a plain file, like a link, is removed and
// treated as no version at all.
func readRequest(root *os.Root) (string, bool) {
	fi, err := root.Lstat(RequestFile)
	if err != nil {
		return "", false
	}
	defer root.Remove(RequestFile)
	if !fi.Mode().IsRegular() {
		return "", true
	}
	f, err := root.Open(RequestFile)
	if err != nil {
		return "", true
	}
	defer f.Close()
	b, _ := io.ReadAll(io.LimitReader(f, 64))
	return strings.TrimSpace(string(b)), true
}

type statusWriter struct {
	root *os.Root
	log  io.Writer
}

// write replaces the status file whole: written to a new file beside it,
// then renamed over it. Creating the new file refuses one that's already
// there, and a rename replaces a link rather than writing through it.
func (w statusWriter) write(s Status) {
	s.At = time.Now()
	if w.log != nil {
		fmt.Fprintf(w.log, "pierd upgrade-apply: %s %s %s%s\n", s.State, s.Version, s.Step, s.Error)
	}
	b, _ := json.Marshal(s)
	name := fmt.Sprintf(".status.%d.%d", os.Getpid(), time.Now().UnixNano())
	f, err := w.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return
	}
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = w.root.Rename(name, StatusFile)
	}
	if err != nil {
		w.root.Remove(name)
	}
}

func runVersion(ctx context.Context, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version").Output()
	return string(out), err
}

// sentence ends an error with a full stop.
func sentence(err error) string {
	s := err.Error()
	if !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s
}
