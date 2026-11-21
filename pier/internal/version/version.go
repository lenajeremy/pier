// Package version says which release of Pier this binary is. pier and
// pierd share it, so each can report its own and compare with the other's.
package version

import (
	"runtime/debug"
	"strings"
)

// Version is set when a release is built:
//
//	go build -ldflags "-X pier/internal/version.Version=v0.6.0"
//
// A build without it reports the commit it was built from instead.
var Version = ""

// String is the release, such as v0.6.0, or for a build from source
// dev-3f9a2c1, with -dirty when the checkout had uncommitted changes.
func String() string {
	if Version != "" {
		return Version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	var rev string
	var dirty bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return "dev"
	}
	v := "dev-" + rev[:min(7, len(rev))]
	if dirty {
		v += "-dirty"
	}
	return v
}

// Release reports whether v names a published release rather than a build
// from source.
func Release(v string) bool {
	return strings.HasPrefix(v, "v") && !strings.Contains(v, "-dirty")
}
