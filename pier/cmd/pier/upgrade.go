package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"pier/internal/release"
	"pier/internal/version"
)

// Where releases come from. They're fixed: only tests change them.
var (
	releaseBase = release.DownloadBase
	latestAPI   = release.LatestAPI
	// executable is the pier that's running, the one an upgrade replaces.
	executable = func() (string, error) {
		exe, err := os.Executable()
		if err != nil {
			return "", err
		}
		return filepath.EvalSymlinks(exe)
	}
)

// upgrade replaces this pier with the latest release, or with --server asks
// pierd to update itself.
func upgradeCmd(args []string) error {
	fs := flag.NewFlagSet("upgrade", flag.ExitOnError)
	server := fs.Bool("server", false, "update pierd on the server instead (the owner only)")
	fs.Parse(args)
	if *server {
		return upgradeServer()
	}
	return upgradeCLI()
}

// upgradeCLI downloads the latest CLI bundle for this computer, checks it
// against its SHA256SUMS and that it runs here, then puts it over the
// running pier: copied beside it, then renamed, so there's no moment
// without a pier.
func upgradeCLI() error {
	mine := version.String()
	if !release.Valid(mine) {
		return fmt.Errorf("this pier is a build from source (%s), so pier upgrade leaves it alone; install a release with:\n  %s", mine, strings.Join(installCLI, " "))
	}
	exe, err := executable()
	if err != nil {
		return fmt.Errorf("pier can't tell where it's installed: %w", err)
	}
	if err := writable(filepath.Dir(exe)); err != nil {
		return fmt.Errorf("this pier is in %s, which it can't write to, so it can't replace itself. Run it with sudo:\n  sudo %s upgrade\nor reinstall with:\n  %s",
			filepath.Dir(exe), exe, strings.Join(installCLI, " "))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	client := release.Client(10 * time.Minute)
	stop := spin("Checking for a new release")
	latest, err := release.Latest(ctx, client, latestAPI)
	stop()
	if err != nil {
		return err
	}
	if !release.Newer(latest, mine) {
		fmt.Printf("%s pier %s is up to date.\n", tick(), mine)
		return nil
	}

	bundle := "pier-cli-" + runtime.GOOS + "-" + runtime.GOARCH
	work, err := os.MkdirTemp("", "pier-upgrade-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	stop = spin("Downloading pier " + latest)
	dir, err := release.Fetch(ctx, client, releaseBase+"/"+latest+"/"+bundle+".tar.gz", bundle, work)
	stop()
	if err != nil {
		return err
	}
	if _, err := release.Verify(dir, "pier", "OS", "ARCH", "VERSION"); err != nil {
		return fmt.Errorf("%w. pier wasn't changed", err)
	}
	switch {
	case release.ReadValue(dir, "OS") != runtime.GOOS || release.ReadValue(dir, "ARCH") != runtime.GOARCH:
		return fmt.Errorf("the download is for another kind of computer, so pier wasn't changed")
	case release.ReadValue(dir, "VERSION") != latest:
		return fmt.Errorf("the download says it's %s, not %s, so pier wasn't changed", release.ReadValue(dir, "VERSION"), latest)
	}
	// It has to run here before it replaces the pier that does.
	out, err := exec.CommandContext(ctx, filepath.Join(dir, "pier"), "--version").Output()
	if err != nil || strings.TrimSpace(string(out)) != "pier "+latest {
		return fmt.Errorf("the new pier didn't run on this computer, so pier wasn't changed")
	}
	if err := release.Replace(filepath.Join(dir, "pier"), exe, 0o755); err != nil {
		return fmt.Errorf("couldn't put the new pier in place, so pier wasn't changed: %w", err)
	}
	fmt.Printf("%s pier is now %s, up from %s. What's new: %s\n", tick(), latest, mine, release.Notes(latest))
	return nil
}

// writable says whether pier can make a file in dir, the way it will to
// replace itself.
func writable(dir string) error {
	f, err := os.CreateTemp(dir, ".pier.")
	if err != nil {
		return err
	}
	f.Close()
	return os.Remove(f.Name())
}

type upgradeInfo struct {
	Running string `json:"running"`
	Latest  string `json:"latest"`
	Helper  bool   `json:"helper"`
	Command string `json:"command"`
	Pending bool   `json:"pending"`
	Status  *struct {
		State, Version, Step, Error string
	} `json:"status"`
}

// upgradeServer asks pierd to update itself to the latest release, then
// follows the update until the new pierd answers, or says why it failed.
func upgradeServer() error {
	c, err := signedIn()
	if err != nil {
		return err
	}
	ctx := context.Background()
	if who, err := whoAmI(ctx, c); err == nil && who.Role != "owner" {
		return errors.New("Only the owner can update the server.")
	}
	var info upgradeInfo
	if err := callWith("Asking pierd about updates", c, http.MethodGet, "/api/server/upgrade", nil, &info); err != nil {
		if strings.Contains(err.Error(), "no such API endpoint") {
			return fmt.Errorf("this pierd can't update itself yet. On the server, update it once with:\n  %s", strings.Join(installServer, " "))
		}
		return err
	}
	if !info.Helper {
		return fmt.Errorf("Run the installer once more on the server to turn on updates from here:\n  %s", info.Command)
	}
	target := info.Latest
	if target == "" {
		// pierd asks GitHub every few hours; this asks now.
		stop := spin("Checking for a new release")
		target, err = release.Latest(ctx, release.Client(30*time.Second), latestAPI)
		stop()
		if err != nil {
			return err
		}
	}
	if !release.Newer(target, info.Running) {
		fmt.Printf("%s pierd %s is up to date.\n", tick(), info.Running)
		return nil
	}
	if err := callWith("Asking pierd to update to "+target, c, http.MethodPost, "/api/server/upgrade", map[string]string{"version": target}, nil); err != nil {
		return err
	}
	return followUpgrade(c, info.Running, target)
}

// followUpgrade shows each step the helper reports. While pierd restarts it
// can't answer, so it keeps asking until the new pierd does.
func followUpgrade(c config, from, target string) error {
	deadline := time.Now().Add(15 * time.Minute)
	label := "Waiting for the update to start"
	stop := spin(label)
	defer func() { stop() }()
	show := func(l string) {
		if l != label {
			stop()
			label = l
			stop = spin(l)
		}
	}
	for time.Now().Before(deadline) {
		time.Sleep(time.Second)
		if v, err := serverVersion(c); err == nil && v == target {
			stop()
			fmt.Printf("%s pierd is now %s, up from %s. What's new: %s\n", tick(), target, from, release.Notes(target))
			return nil
		}
		var info upgradeInfo
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := callContext(ctx, c, http.MethodGet, "/api/server/upgrade", nil, &info)
		cancel()
		switch {
		case err != nil:
			show("pierd is restarting")
		case info.Status != nil && info.Status.State == "failed":
			stop()
			return fmt.Errorf("the update didn't happen: %s", info.Status.Error)
		case info.Status != nil && info.Status.Step != "":
			show(info.Status.Step)
		}
	}
	return fmt.Errorf("pierd didn't answer as %s within 15 minutes; check it on the server with: sudo systemctl status pierd pierd-upgrade", target)
}
