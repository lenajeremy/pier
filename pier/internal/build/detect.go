// Package build works out how to build what was pushed, and runs the build in
// a throwaway container (or, for development, on the host).
package build

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Plan says how to turn a checkout into static files.
type Plan struct {
	// Label names what was found, for the log: "astro 4.16", "Static HTML".
	Label string
	// Source says where it was found: "package.json", "index.html".
	Source string
	// Install and Build are shell commands run in the checkout; empty means
	// nothing to run.
	Install string
	Build   string
	// Output is the folder, relative to the checkout, that gets served.
	Output string
}

// ErrNothingToServe means the push has nothing Pier knows how to serve yet.
var ErrNothingToServe = errors.New("nothing to serve: add a package.json with a build script, or an index.html")

type packageJSON struct {
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

// frameworks, checked in order: the first dependency found wins.
var frameworks = []struct{ dep, name, output string }{
	{"astro", "astro", "dist"},
	{"next", "next", "out"},
	{"@sveltejs/kit", "sveltekit", "build"},
	{"@11ty/eleventy", "eleventy", "_site"},
	{"vite", "vite", "dist"},
	{"react-scripts", "create-react-app", "build"},
}

// Detect looks at a checkout and decides how to build it.
func Detect(dir string) (Plan, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if errors.Is(err, os.ErrNotExist) {
		return detectStatic(dir)
	}
	if err != nil {
		return Plan{}, err
	}
	var pkg packageJSON
	if err := json.Unmarshal(raw, &pkg); err != nil {
		return Plan{}, fmt.Errorf("package.json: %w", err)
	}
	if pkg.Scripts["build"] == "" {
		if p, err := detectStatic(dir); err == nil {
			return p, nil
		}
		return Plan{}, errors.New(`package.json has no "build" script`)
	}

	p := Plan{Label: "node build", Source: "package.json", Output: "dist"}
	for _, f := range frameworks {
		if v, ok := dep(pkg, f.dep); ok {
			p.Label = f.name
			if v != "" {
				p.Label += " " + v
			}
			p.Output = f.output
			break
		}
	}
	p.Install, p.Build = commands(dir)
	return p, nil
}

func detectStatic(dir string) (Plan, error) {
	for _, out := range []string{".", "public"} {
		if _, err := os.Stat(filepath.Join(dir, out, "index.html")); err == nil {
			src := "index.html"
			if out != "." {
				src = out + "/index.html"
			}
			return Plan{Label: "static HTML", Source: src, Output: out}, nil
		}
	}
	return Plan{}, ErrNothingToServe
}

// dep finds a dependency and returns its major.minor version, "4.16" for "^4.16.2".
func dep(pkg packageJSON, name string) (string, bool) {
	v, ok := pkg.Dependencies[name]
	if !ok {
		v, ok = pkg.DevDependencies[name]
	}
	if !ok {
		return "", false
	}
	v = strings.TrimLeft(v, "^~>=< v")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) >= 2 && isNum(parts[0]) && isNum(parts[1]) {
		return parts[0] + "." + parts[1], true
	}
	if len(parts) >= 1 && isNum(parts[0]) {
		return parts[0], true
	}
	return "", true
}

func isNum(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// commands picks the install and build commands from the lockfile present.
func commands(dir string) (install, build string) {
	has := func(f string) bool { _, err := os.Stat(filepath.Join(dir, f)); return err == nil }
	switch {
	case has("pnpm-lock.yaml"):
		return "corepack enable && pnpm install --frozen-lockfile", "pnpm run build"
	case has("yarn.lock"):
		return "corepack enable && yarn install --frozen-lockfile", "yarn run build"
	case has("bun.lockb"), has("bun.lock"):
		return "npm install -g bun && bun install --frozen-lockfile", "bun run build"
	case has("package-lock.json"):
		return "npm ci", "npm run build"
	default:
		return "npm install", "npm run build"
	}
}
