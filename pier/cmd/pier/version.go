package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"pier/internal/release"
	"pier/internal/version"
)

// The one-line installers, which fetch the latest release, as the parts
// they're printed in: a command this long would wrap mid-word in an
// ordinary terminal, so it's split the way a shell script splits one.
var (
	installCLI    = []string{"curl -fsSL", "https://raw.githubusercontent.com/lenajeremy/pier/main/pier/install/cli.sh", "| sh"}
	installServer = []string{"curl -fsSL", "https://raw.githubusercontent.com/lenajeremy/pier/main/pier/install/get.sh", "| sudo sh"}
)

// printCommand prints a command a part a line, each continued with a
// backslash, so pasting it still runs it.
func printCommand(parts []string, color bool) {
	for i, p := range parts {
		line := "  "
		if i > 0 {
			line += "    "
		}
		line += p
		if i < len(parts)-1 {
			line += " \\"
		}
		fmt.Println(ink(line, fgSecondary, color))
	}
}

// showVersion prints this pier's release and, when it's signed in, the
// release of the pierd it talks to, and says how to update whichever is
// behind. --version prints only this pier's, without asking the server.
func showVersion(args []string) error {
	mine := version.String()
	if len(args) > 0 && (args[0] == "--short" || args[0] == "-s") {
		fmt.Println(mine)
		return nil
	}
	c := loadConfig()
	color := colorEnabled()
	row := func(name, v, detail string) {
		line := "  " + boldInk(fmt.Sprintf("%-6s", name), fgBright, color) + "  " + ink(v, fgSecondary, color)
		if detail != "" {
			line += "  " + ink(detail, fgDim, color)
		}
		fmt.Println(line)
	}
	fmt.Println()
	row("pier", mine, "this computer")
	if c.Server == "" || c.Token == "" {
		row("pierd", "unknown", "not signed in: pier login ADDRESS")
		fmt.Println()
		return nil
	}
	server, err := serverInfo(c)
	theirs := server.Version
	switch {
	case errors.Is(err, errNoVersion):
		row("pierd", "older than v0.6.0", host(c.Server))
		theirs = "v0.5.0"
	case err != nil:
		row("pierd", "unknown", err.Error())
		fmt.Println()
		return nil
	default:
		row("pierd", theirs, host(c.Server))
	}
	fmt.Println()
	if !version.Release(mine) || !version.Release(theirs) {
		return nil
	}
	// Behind the other, or behind the newest release pierd has heard of.
	latest := server.Latest
	pierBehind := newer(theirs, mine) || release.Newer(latest, mine)
	pierdBehind := newer(mine, theirs) || release.Newer(latest, theirs)
	if release.Valid(latest) && (pierBehind || pierdBehind) {
		fmt.Println("  " + ink(latest+" is out: "+release.Notes(latest), fgMuted, color))
	}
	switch {
	case pierdBehind && server.Upgrade:
		// A pierd that can update itself is asked to, by its owner.
		fmt.Println("  " + ink("pierd is behind. As the owner, update it with:", fgMuted, color))
		printCommand([]string{"pier upgrade --server"}, color)
	case pierdBehind:
		fmt.Println("  " + ink("pierd is behind. On the server, update it with:", fgMuted, color))
		printCommand(installServer, color)
	}
	if pierBehind {
		fmt.Println("  " + ink("pier is behind. Update it with:", fgMuted, color))
		printCommand([]string{"pier upgrade"}, color)
	}
	if pierBehind || pierdBehind {
		fmt.Println()
	}
	return nil
}

// errNoVersion means pierd predates version reporting.
var errNoVersion = errors.New("pierd doesn't report its version")

func serverVersion(c config) (string, error) {
	info, err := serverInfo(c)
	return info.Version, err
}

// versionAnswer is what pierd says about its release. Upgrade means it can
// update itself, with pier upgrade --server.
type versionAnswer struct {
	Version string `json:"version"`
	Latest  string `json:"latest"`
	Upgrade bool   `json:"upgrade"`
}

func serverInfo(c config) (versionAnswer, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var none versionAnswer
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Server+"/api/version", nil)
	if err != nil {
		return none, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := client.Do(req)
	if err != nil {
		return none, fmt.Errorf("can't reach %s", host(c.Server))
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return none, errNoVersion
	case resp.StatusCode == http.StatusUnauthorized:
		return none, fmt.Errorf("signed out: pier login %s", host(c.Server))
	case resp.StatusCode != http.StatusOK:
		return none, fmt.Errorf("%s answered %s", host(c.Server), resp.Status)
	}
	var v versionAnswer
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&v); err != nil || v.Version == "" {
		return none, errNoVersion
	}
	return v, nil
}

// newer reports whether release a is later than release b, both like
// v1.2.3.
func newer(a, b string) bool {
	pa, pb := parts(a), parts(b)
	for i := range 3 {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func parts(v string) [3]int {
	var out [3]int
	core, _, _ := strings.Cut(strings.TrimPrefix(v, "v"), "-")
	for i, p := range strings.SplitN(core, ".", 3) {
		out[i], _ = strconv.Atoi(p)
	}
	return out
}
