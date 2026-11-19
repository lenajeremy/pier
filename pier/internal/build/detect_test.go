package build

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDetect(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  Plan
	}{
		{"astro with npm lockfile", map[string]string{
			"package.json":      `{"scripts":{"build":"astro build"},"dependencies":{"astro":"^4.16.2"}}`,
			"package-lock.json": `{}`,
		}, Plan{Label: "astro 4.16", Source: "package.json", Install: "npm ci", Build: "npm run build", Output: "dist"}},
		{"vite with pnpm", map[string]string{
			"package.json":   `{"scripts":{"build":"vite build"},"devDependencies":{"vite":"~6.0.1"}}`,
			"pnpm-lock.yaml": ``,
		}, Plan{Label: "vite 6.0", Source: "package.json", Install: "corepack pnpm@10 install --frozen-lockfile", Build: "corepack pnpm@10 run build", Output: "dist",
			Tooling: "pnpm 10, since package.json doesn't name a version (add packageManager to pin one)"}},
		{"next static export", map[string]string{
			"package.json": `{"scripts":{"build":"next build"},"dependencies":{"next":"15.1.0"}}`,
			"yarn.lock":    ``,
		}, Plan{Label: "next 15.1", Source: "package.json", Install: "corepack yarn@1 install --frozen-lockfile", Build: "corepack yarn@1 run build", Output: "out",
			Tooling: "yarn 1, since package.json doesn't name a version (add packageManager to pin one)"}},
		{"unknown builder", map[string]string{
			"package.json": `{"scripts":{"build":"node build.js"}}`,
		}, Plan{Label: "node build", Source: "package.json", Install: "npm install", Build: "npm run build", Output: "dist"}},
		{"plain html", map[string]string{"index.html": "<h1>hi</h1>"},
			Plan{Label: "static HTML", Source: "index.html", Output: "."}},
		{"html in public", map[string]string{"public/index.html": "<h1>hi</h1>", "README.md": "x"},
			Plan{Label: "static HTML", Source: "public/index.html", Output: "public"}},
		{"package.json without a build, plus html", map[string]string{
			"package.json": `{"name":"x"}`, "index.html": "<h1>hi</h1>",
		}, Plan{Label: "static HTML", Source: "index.html", Output: "."}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, c.files)
			got, err := Detect(dir)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Fatalf("got  %+v\nwant %+v", got, c.want)
			}
		})
	}
}

func TestDetectNothing(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{"README.md": "hello"})
	if _, err := Detect(dir); err != ErrNothingToServe {
		t.Fatalf("got %v", err)
	}
	write(t, dir, map[string]string{"package.json": `{"name":"x"}`})
	if _, err := Detect(dir); err == nil {
		t.Fatal("a package.json without a build script should fail")
	}
}

func TestPackageManagerVersion(t *testing.T) {
	vite := `"scripts":{"build":"vite build"},"devDependencies":{"vite":"^8.3.0"}`
	cases := []struct {
		name, pkg, lock, lockBody, tools, install, note string
	}{
		{"packageManager wins over the computer's", `{` + vite + `,"packageManager":"pnpm@9.15.0+sha512.abc"}`, "pnpm-lock.yaml", "", "pnpm@10.33.0",
			"corepack pnpm@9.15.0 install --frozen-lockfile", "pnpm 9.15.0, from packageManager in package.json"},
		{"the computer's version", `{` + vite + `}`, "pnpm-lock.yaml", "", "pnpm@10.33.0",
			"corepack pnpm@10.33.0 install --frozen-lockfile", "pnpm 10.33.0, the version on your computer"},
		{"a hint for another tool is ignored", `{` + vite + `}`, "pnpm-lock.yaml", "", "yarn@1.22.22",
			"corepack pnpm@10 install --frozen-lockfile", "pnpm 10, since package.json doesn't name a version (add packageManager to pin one)"},
		{"yarn 2 and later install immutably", `{` + vite + `}`, "yarn.lock", "__metadata:\n  version: 8\n", "",
			"corepack yarn@4 install --immutable", "yarn 4, since package.json doesn't name a version (add packageManager to pin one)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, map[string]string{"package.json": c.pkg, c.lock: c.lockBody})
			got, err := DetectWith(dir, c.tools)
			if err != nil {
				t.Fatal(err)
			}
			if got.Install != c.install || got.Tooling != c.note {
				t.Fatalf("got %q (%s)\nwant %q (%s)", got.Install, got.Tooling, c.install, c.note)
			}
		})
	}
	if ValidTools("pnpm@10; rm -rf /") || !ValidTools("pnpm@10.33.0") || !ValidTools("") {
		t.Fatal("ValidTools lets the wrong things through")
	}
}
