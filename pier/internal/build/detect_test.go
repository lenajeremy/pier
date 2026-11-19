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
		}, Plan{Label: "vite 6.0", Source: "package.json", Install: "corepack enable && pnpm install --frozen-lockfile", Build: "pnpm run build", Output: "dist"}},
		{"next static export", map[string]string{
			"package.json": `{"scripts":{"build":"next build"},"dependencies":{"next":"15.1.0"}}`,
			"yarn.lock":    ``,
		}, Plan{Label: "next 15.1", Source: "package.json", Install: "corepack enable && yarn install --frozen-lockfile", Build: "yarn run build", Output: "out"}},
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
