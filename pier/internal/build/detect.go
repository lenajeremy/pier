// Package build works out how to build what was pushed, and runs the build in
// a throwaway container (or, for development, on the host).
package build

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Plan describes a static build or an HTTP application image.
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
	// Tooling says which package manager version runs, and why.
	Tooling string
	Runtime string // server or dockerfile for an HTTP process
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
func Detect(dir string) (Plan, error) { return DetectWith(dir, "") }

// DetectWith is Detect with a hint of the package manager the developer uses.
func DetectWith(dir, tools string) (Plan, error) {
	if fi, err := os.Stat(filepath.Join(dir, "Dockerfile")); err == nil && fi.Mode().IsRegular() {
		return Plan{Label: "Dockerfile", Source: "Dockerfile", Runtime: "dockerfile"}, nil
	}
	return detectPackage(dir, tools)
}

func detectPackage(dir, tools string) (Plan, error) {
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
		if pkg.Scripts["start"] != "" {
			return Plan{Label: "Railpack server", Source: "package.json", Runtime: "server"}, nil
		}
		return Plan{}, errors.New(`package.json has no "build" or "start" script`)
	}

	p := Plan{Label: "node build", Source: "package.json", Output: "dist"}
	for _, f := range frameworks {
		if v, ok := dep(pkg, f.dep); ok {
			p.Label = f.name
			if v != "" {
				p.Label += " " + v
			}
			if f.dep == "next" && pkg.Scripts["start"] != "" && !nextStaticExport(dir) {
				return Plan{Label: p.Label, Source: "package.json", Runtime: "server"}, nil
			}
			p.Output = f.output
			break
		}
	}
	p.Install, p.Build, p.Tooling = commands(dir, tools)
	return p, nil
}

var nextExportRe = regexp.MustCompile(`\boutput\s*:\s*['"]export['"]`)

// nextStaticExport reports whether a Next.js config explicitly requests a static export.
func nextStaticExport(dir string) bool {
	for _, name := range []string{"next.config.ts", "next.config.mjs", "next.config.js", "next.config.cjs"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if i := strings.Index(line, "//"); i >= 0 {
				line = line[:i]
			}
			if nextExportRe.MatchString(line) {
				return true
			}
		}
	}
	return false
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
// detects. Preset selects an explicit static or server build.
type Overrides struct {
	Root, Preset, Install, Build, Output string
}

// Empty reports whether nothing is overridden.
func (o Overrides) Empty() bool { return o == Overrides{} }

// Presets Pier knows how to build today.
var Presets = []string{"static", "vite", "server", "dockerfile"}

// PlanFor works out how to build dir with the owner's overrides: it detects
// in the root folder, or uses the forced preset, then applies any command or
// folder the owner set. It returns the root, relative to dir.
func PlanFor(dir string, o Overrides, tools string) (Plan, string, error) {
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
	case "server":
		p = Plan{Label: "Railpack server", Source: "your settings", Runtime: "server"}
	case "dockerfile":
		p = Plan{Label: "Dockerfile", Source: "Dockerfile", Runtime: "dockerfile"}
	case "static":
		p, err = detectStatic(at)
		if err != nil {
			p, err = Plan{Label: "static HTML", Output: "."}, nil
		}
		p.Source = "your settings"
	case "vite":
		p = Plan{Label: "vite", Source: "your settings", Output: "dist"}
		p.Install, p.Build, p.Tooling = commands(at, tools)
	case "":
		if o.Output != "" || o.Install != "" || o.Build != "" {
			p, err = detectPackage(at, tools)
		} else {
			p, err = DetectWith(at, tools)
		}
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
	if p.Runtime != "" {
		if o.Output != "" || o.Install != "" || p.Runtime == "dockerfile" && o.Build != "" {
			return Plan{}, "", errors.New("server builds use the image start command; configure install and output in railpack.json or Dockerfile, or select a static preset")
		}
		p.Build = o.Build
		return p, root, nil
	}
	if o.Install != "" {
		p.Install = o.Install
	}
	if o.Build != "" {
		p.Build = o.Build
		if _, err := os.Stat(filepath.Join(at, "package.json")); err == nil && p.Install == "" {
			p.Install, _, p.Tooling = commands(at, tools)
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

// Tools names the package manager the developer uses, like "pnpm@10.33.0",
// as pier deploy reports it. It's only a hint: package.json's packageManager
// field wins.
var toolRe = regexp.MustCompile(`^(pnpm|yarn|bun)@([0-9][0-9A-Za-z.+-]{0,40})$`)

// ValidTools reports whether a hint from the CLI is well formed.
func ValidTools(s string) bool { return s == "" || toolRe.MatchString(s) }

// defaults are the versions used when nothing says which: the major the
// lockfile format belongs to, not whatever is newest, so a build doesn't
// break because the package manager changed under it.
var defaults = map[string]string{"pnpm": "10", "yarn": "1", "yarn-berry": "4"}

// commands picks the install and build commands from the lockfile present,
// with the package manager at the version the project or the developer
// uses. note says which version and why, for the log.
func commands(dir, tools string) (install, build, note string) {
	has := func(f string) bool { _, err := os.Stat(filepath.Join(dir, f)); return err == nil }
	version := func(tool, def string) (string, string) {
		if v := packageManagerField(dir, tool); v != "" {
			return v, tool + " " + v + ", from packageManager in package.json"
		}
		if m := toolRe.FindStringSubmatch(tools); m != nil && m[1] == tool {
			return m[2], tool + " " + m[2] + ", the version on your computer"
		}
		return def, tool + " " + def + ", since package.json doesn't name a version (add packageManager to pin one)"
	}
	switch {
	case has("pnpm-lock.yaml"):
		v, n := version("pnpm", defaults["pnpm"])
		run := "corepack pnpm@" + v
		return run + " install --frozen-lockfile", run + " run build", n
	case has("yarn.lock"):
		lock, _ := os.ReadFile(filepath.Join(dir, "yarn.lock"))
		if strings.Contains(string(lock), "__metadata:") { // Yarn 2 and later
			v, n := version("yarn", defaults["yarn-berry"])
			run := "corepack yarn@" + v
			return run + " install --immutable", run + " run build", n
		}
		v, n := version("yarn", defaults["yarn"])
		run := "corepack yarn@" + v
		return run + " install --frozen-lockfile", run + " run build", n
	case has("bun.lockb"), has("bun.lock"):
		v, n := version("bun", "latest")
		return "npm install -g bun@" + v + " && bun install --frozen-lockfile", "bun run build", n
	case has("package-lock.json"):
		return "npm ci", "npm run build", ""
	default:
		return "npm install", "npm run build", ""
	}
}

// packageManagerField reads package.json's packageManager when it names
// tool: "pnpm@9.15.0+sha512..." gives "9.15.0".
func packageManagerField(dir, tool string) string {
	raw, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return ""
	}
	var pkg struct {
		PackageManager string `json:"packageManager"`
	}
	if json.Unmarshal(raw, &pkg) != nil {
		return ""
	}
	name, v, ok := strings.Cut(pkg.PackageManager, "@")
	if !ok || name != tool {
		return ""
	}
	v, _, _ = strings.Cut(v, "+")
	if !toolRe.MatchString(tool + "@" + v) {
		return ""
	}
	return v
}
