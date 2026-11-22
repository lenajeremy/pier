package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"pier/internal/release"
	"pier/internal/upgrade"
	"pier/internal/version"
)

// upgradeApply is pierd upgrade-apply, the root helper that
// pierd-upgrade.service runs when pierd asks for an update. It reads only a
// version from the request, and downloads that release from the fixed
// release address; -release-base exists for trying it against a local
// release server, and only root's unit file can set it.
func upgradeApply(args []string) int {
	fs := flag.NewFlagSet("upgrade-apply", flag.ExitOnError)
	data := dataFlag(fs)
	bin := fs.String("bin", "/usr/local/bin", "where pier and pierd are installed")
	units := fs.String("units", "/etc/systemd/system", "where pierd's unit files are")
	base := fs.String("release-base", release.DownloadBase, "where releases are downloaded from; for testing only")
	restart := fs.Bool("restart", true, "reload systemd and restart pierd once installed; false only installs")
	fs.Parse(args)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	a := upgrade.Apply{
		Data: *data, Bin: *bin, Units: *units, Base: *base, Arch: runtime.GOARCH,
		Current: version.String(), Log: os.Stderr,
	}
	if *restart {
		a.Systemctl = func(args ...string) error {
			out, err := exec.CommandContext(ctx, "systemctl", args...).CombinedOutput()
			if err != nil {
				return fmt.Errorf("systemctl %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
			}
			return nil
		}
	}
	if err := a.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "pierd:", err)
		return 1
	}
	return 0
}
