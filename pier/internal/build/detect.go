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

// Overrides are the owner's build settings. Empty fields keep what Pier
// detects; Preset forces one ("static" or "vite").
type Overrides struct {
	Root, Preset, Install, Build, Output string
}

// Empty reports whether nothing is overridden.
func (o Overrides) Empty() bool { return o == Overrides{} }

// Presets Pier knows how to build today.
var Presets = []string{"static", "vite"}

// PlanFor works out how to build dir with the owner's overrides: it detects
// in the root folder, or uses the forced preset, then applies any command or
// folder the owner set. It returns the root, relative to dir.
func PlanFor(dir string, o Overrides) (Plan, string, error) {
	root, err := Folder(o.Root)
	if err != nil {
		return Plan{}, "", fmt.Errorf("root directory: %w", err)
	}
	at := filepath.Join(dir, root)
	if fi, err := os.Stat(at); err != nil || !fi.IsDir() {
		return Plan{}, "", fmt.Errorf("the root directory %s/ isn't in what was deployed", root)
	}
	var p Plan
	switch o.Preset {
	case "static":
		p, err = detectStatic(at)
		if err != nil {
			p, err = Plan{Label: "static HTML", Output: "."}, nil
		}
		p.Source = "your settings"
	case "vite":
		p = Plan{Label: "vite", Source: "your settings", Output: "dist"}
		p.Install, p.Build = commands(at)
	case "":
		p, err = Detect(at)
		if err != nil && o.Build != "" {
			// Nothing detected, but the owner says how to build it.
			p, err = Plan{Label: "your build", Source: "your settings", Output: "dist"}, nil
		}
	default:
		return Plan{}, "", fmt.Errorf("unknown preset %q", o.Preset)
	}
	if err != nil {
		return Plan{}, "", err
	}
	if o.Install != "" {
		p.Install = o.Install
	}
	if o.Build != "" {
		p.Build = o.Build
		if _, err := os.Stat(filepath.Join(at, "package.json")); err == nil && p.Install == "" {
			p.Install, _ = commands(at)
		}
	}
	if o.Output != "" {
		out, err := Folder(o.Output)
		if err != nil {
			return Plan{}, "", fmt.Errorf("output folder: %w", err)
		}
		p.Output = out
	}
	return p, root, nil
}

// Folder cleans a folder the owner typed and checks it stays inside the
// project: "./web/" becomes "web", "" becomes ".".
func Folder(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" || p == "./" || p == "." {
		return ".", nil
	}
	if filepath.IsAbs(p) {
		return "", errors.New("use a folder inside the project, like web")
	}
	c := filepath.Clean(p)
	if c == ".." || strings.HasPrefix(c, "../") {
		return "", errors.New("use a folder inside the project, like web")
	}
	return c, nil
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
