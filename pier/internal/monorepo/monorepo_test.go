package monorepo

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
)

func TestFoldersAndKinds(t *testing.T) {
	folders, truncated := Folders([]string{
		"package.json", "pnpm-workspace.yaml", "turbo.json", "README.md",
		"apps/web/next.config.mjs", "apps/web/package.json", "apps/web/public/index.html", "apps/web/src/page.tsx",
		"apps/web-admin/vite.config.ts", "apps/web-admin/package.json", "apps/web-admin/index.html",
		"apps/shop/svelte.config.js", "apps/shop/vite.config.ts", "apps/shop/package.json",
		"apps/blog/astro.config.mjs", "apps/store/nuxt.config.ts", "apps/remix/remix.config.js",
		"apps/docs/docusaurus.config.js", "apps/worker/package.json",
		"services/api/go.mod", "services/api/Dockerfile", "services/rust/Cargo.toml",
		"services/py/pyproject.toml", "services/ml/requirements.txt", "services/rails/Gemfile",
		"services/php/composer.json", "services/deno/deno.jsonc", "services/box/Dockerfile",
		"packages/ui/package.json", "packages/ui/src/button.tsx", "libs/core/package.json",
		"site/index.html", "tools/scripts/run.sh",
		// None of these is anyone's source.
		".github/workflows/ci.yml", "apps/web/.env.example", "node_modules/react/package.json",
		"apps/web/node_modules/next/package.json", "vendor/lib/go.mod", "dist/index.html",
		"apps/web/build/index.html", "apps/web/.next/server.js", "services/rust/target/debug/app",
		"apps/web/next.config", // no extension, so not a Next.js config
	})
	if truncated {
		t.Fatal("a small repository was truncated")
	}
	want := []Folder{
		{"", "Workspace", false},
		{"apps", "", false},
		{"apps/blog", "Astro", true},
		{"apps/docs", "Docusaurus", true},
		{"apps/remix", "Remix", true},
		{"apps/shop", "SvelteKit", true},
		{"apps/store", "Nuxt", true},
		{"apps/web", "Next.js", true},
		{"apps/web/public", "Static", false},
		{"apps/web/src", "", false},
		{"apps/web-admin", "Vite", true},
		{"apps/worker", "Node", true},
		{"libs", "", false},
		{"libs/core", "Node", false},
		{"packages", "", false},
		{"packages/ui", "Node", false},
		{"packages/ui/src", "", false},
		{"services", "", false},
		{"services/api", "Dockerfile", true},
		{"services/box", "Dockerfile", true},
		{"services/deno", "Deno", true},
		{"services/ml", "Python", true},
		{"services/php", "PHP", true},
		{"services/py", "Python", true},
		{"services/rails", "Ruby", true},
		{"services/rust", "Rust", true},
		{"site", "Static", true},
		{"tools", "", false},
		{"tools/scripts", "", false},
	}
	if !slices.Equal(folders, want) {
		t.Fatalf("folders:\n%s\nwant:\n%s", list(folders), list(want))
	}
}

func list(fs []Folder) string {
	var b strings.Builder
	for _, f := range fs {
		fmt.Fprintf(&b, "  %q %q %v\n", f.Path, f.Kind, f.App)
	}
	return b.String()
}

func TestFolderKindsAtTheRoot(t *testing.T) {
	for files, want := range map[string]string{
		"package.json":                 "Node",
		"package.json nx.json":         "Workspace",
		"package.json lerna.json":      "Workspace",
		"go.mod Dockerfile":            "Dockerfile",
		"index.html package.json":      "Node",
		"index.html":                   "Static",
		"README.md":                    "",
		"next.config.js package.json":  "Next.js",
		"deno.json":                    "Deno",
		"requirements.txt main.py":     "Python",
		"Cargo.toml src/main.rs":       "Rust",
		"vite.config.js svelte.config": "Vite",
	} {
		folders, _ := Folders(strings.Fields(files))
		if folders[0].Path != "" || folders[0].Kind != want || folders[0].App {
			t.Errorf("%s: the root is %+v, want kind %q", files, folders[0], want)
		}
	}
	// An empty repository still has its root.
	if folders, truncated := Folders(nil); len(folders) != 1 || folders[0] != (Folder{}) || truncated {
		t.Fatalf("an empty repository lists %+v, %v", folders, truncated)
	}
}

func TestFoldersTruncate(t *testing.T) {
	var files []string
	for i := range MaxFolders + 10 {
		files = append(files, fmt.Sprintf("pages/p%05d/index.html", i))
	}
	folders, truncated := Folders(files)
	if !truncated || len(folders) != MaxFolders {
		t.Fatalf("listed %d folders, truncated %v", len(folders), truncated)
	}
	if folders[0].Path != "" || folders[1].Path != "pages" || folders[2].Path != "pages/p00000" || !folders[2].App {
		t.Fatalf("the first folders are %+v", folders[:3])
	}
	files = files[:MaxFolders-2] // with the root and pages/, exactly the cap
	if folders, truncated = Folders(files); truncated || len(folders) != MaxFolders {
		t.Fatalf("listed %d folders, truncated %v", len(folders), truncated)
	}
}

func TestNpmWorkspaces(t *testing.T) {
	repo := &Repo{Manifests: map[string][]byte{
		"package.json":                        []byte(`{"name":"acme","private":true,"workspaces":["apps/*","packages/**","!packages/legacy"]}`),
		"apps/web/package.json":               []byte(`{"name":"web","dependencies":{"@acme/ui":"*","react":"19"}}`),
		"apps/docs/package.json":              []byte(`{"name":"docs","devDependencies":{"@acme/config":"workspace:*"},"peerDependencies":{"web":"*"}}`),
		"packages/ui/package.json":            []byte(`{"name":"@acme/ui","dependencies":{"@acme/tokens":"1.0.0"},"optionalDependencies":{"@acme/icons":"*"}}`),
		"packages/design/tokens/package.json": []byte(`{"name":"@acme/tokens","dependencies":{"@acme/ui":"*"}}`),
		"packages/icons/package.json":         []byte(`{"name":"@acme/icons"}`),
		"packages/config/package.json":        []byte(`{"name":"@acme/config"}`),
		"packages/legacy/package.json":        []byte(`{"name":"@acme/legacy"}`),
		"tools/package.json":                  []byte(`{"name":"tools"}`),
		"apps/broken/package.json":            []byte(`{"name":`),
	}}
	ws := repo.Workspace()
	for root, want := range map[string][]string{
		"apps/web":    {"packages/design/tokens", "packages/icons", "packages/ui"},
		"./apps/web/": {"packages/design/tokens", "packages/icons", "packages/ui"},
		// The web app it lists as a peer is a member under apps/*.
		"apps/docs":   {"apps/web", "packages/config", "packages/design/tokens", "packages/icons", "packages/ui"},
		"packages/ui": {"packages/design/tokens", "packages/icons"},
		"tools":       nil,
		"apps/none":   nil,
	} {
		if got := ws.Uses(root); !slices.Equal(got, want) {
			t.Errorf("%s uses %q, want %q", root, got, want)
		}
	}
	// A package outside the workspace isn't one of its members, so using it
	// by name means a package from the registry.
	repo.Manifests["apps/site/package.json"] = []byte(`{"name":"site","dependencies":{"tools":"*","@acme/legacy":"*"}}`)
	if got := repo.Workspace().Uses("apps/site"); got != nil {
		t.Fatalf("site uses %q", got)
	}
	// yarn's {"packages": [...]}.
	repo.Manifests["package.json"] = []byte(`{"workspaces":{"packages":["apps/web","packages/ui"],"nohoist":["**/react"]}}`)
	if got := repo.Workspace().Uses("apps/web"); !slices.Equal(got, []string{"packages/ui"}) {
		t.Fatalf("with yarn's workspaces, web uses %q", got)
	}
}

func TestPnpmWorkspaces(t *testing.T) {
	manifests := map[string][]byte{
		"package.json":                   []byte(`{"name":"acme","private":true}`),
		"apps/web/package.json":          []byte(`{"name":"web","dependencies":{"@acme/ui":"workspace:*"}}`),
		"packages/ui/package.json":       []byte(`{"name":"@acme/ui","devDependencies":{"@acme/lint":"workspace:^"}}`),
		"tooling/lint/package.json":      []byte(`{"name":"@acme/lint"}`),
		"tooling/lint/test/package.json": []byte(`{"name":"@acme/lint-fixture"}`),
	}
	for name, yaml := range map[string]string{
		"block": `# the workspace
packages:
  - "apps/*"
  - 'packages/*'   # shared code
  - tooling/**
  - '!**/test/**'

catalog:
  react: ^19
  - not/a/package
`,
		"unindented": "packages:\n- apps/*\n- packages/*\n- tooling/*\nonlyBuiltDependencies:\n  - esbuild\n",
		"flow":       "packages: ['apps/*', \"packages/*\", tooling/lint]\n",
		"crlf":       "packages:\r\n  - apps/*\r\n  - packages/*\r\n  - tooling/lint\r\n",
	} {
		repo := &Repo{Manifests: maps.Clone(manifests)}
		repo.Manifests["pnpm-workspace.yaml"] = []byte(yaml)
		ws := repo.Workspace()
		if got := ws.Uses("apps/web"); !slices.Equal(got, []string{"packages/ui", "tooling/lint"}) {
			t.Errorf("%s: web uses %q", name, got)
		}
		if _, ok := ws.members["@acme/lint-fixture"]; ok {
			t.Errorf("%s: the excluded test package is a member", name)
		}
	}
	if got := pnpmPackages("packages:\n  - a/*\ncatalog:\n  - b/*\n"); !slices.Equal(got, []string{"a/*"}) {
		t.Fatalf("the list ran into the next key: %q", got)
	}
}

func TestAffected(t *testing.T) {
	uses := []string{"packages/ui", "./packages/tokens/"}
	for _, c := range []struct {
		root    string
		changed []string
		want    bool
	}{
		{"", []string{"anything.md"}, true},
		{".", []string{"anything.md"}, true},
		{"apps/web", nil, false},
		{"apps/web", []string{"apps/web/page.tsx"}, true},
		{"apps/web/", []string{"apps/web/page.tsx"}, true},
		{"apps/web", []string{"apps/web"}, true}, // a file where the folder was
		{"apps/web", []string{"apps/website/page.tsx", "apps/we/x", "apps/web-admin/y"}, false},
		{"apps/web", []string{"apps/docs/intro.md"}, false},
		{"apps/web", []string{"packages/ui/button.tsx"}, true},
		{"apps/web", []string{"packages/tokens/colors.css"}, true},
		{"apps/web", []string{"packages/uikit/x.ts"}, false},
		{"apps/web", []string{"docs/package.json", "apps/package.json"}, false},
		{"apps/web", []string{"README.md", ".github/workflows/ci.yml"}, false},
		{"apps/web", []string{"pnpm-lock.yaml"}, true},
		{"apps/web", []string{"./package.json"}, true},
		{"apps/web", []string{"bun.lockb"}, true},
		{"apps/web", []string{".nvmrc"}, true},
		{"apps/web", []string{"tsconfig.base.json"}, true},
		{"apps/web", []string{"turbo.json"}, true},
	} {
		if got := Affected(c.root, uses, c.changed); got != c.want {
			t.Errorf("Affected(%q, %q) = %v, want %v", c.root, c.changed, got, c.want)
		}
	}
}

// archive makes a gzipped tar of files, each under prefix.
func archive(t *testing.T, prefix string, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if prefix != "" {
		tw.WriteHeader(&tar.Header{Typeflag: tar.TypeXGlobalHeader, Name: "pax_global_header", PAXRecords: map[string]string{"comment": "sha"}})
		tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: prefix, Mode: 0o755})
	}
	for _, name := range slices.Sorted(maps.Keys(files)) {
		if err := tw.WriteHeader(&tar.Header{Name: prefix + name, Mode: 0o644, Size: int64(len(files[name])), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(files[name]))
	}
	tw.WriteHeader(&tar.Header{Typeflag: tar.TypeSymlink, Name: prefix + "apps/web/README.md", Linkname: "../../README.md"})
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

var workspaceFilesForTest = map[string]string{
	"package.json":             `{"workspaces":["apps/*","packages/*"]}`,
	"README.md":                "# acme",
	"apps/web/package.json":    `{"name":"web","dependencies":{"@acme/ui":"*"}}`,
	"apps/web/next.config.mjs": "export default {}",
	"packages/ui/package.json": `{"name":"@acme/ui"}`,
	"packages/ui/button.tsx":   "export {}",
}

func TestReadGitHubArchive(t *testing.T) {
	big := `{"name":"big","x":"` + strings.Repeat("x", MaxManifest) + `"}`
	files := maps.Clone(workspaceFilesForTest)
	files["apps/big/package.json"] = big
	repo, err := ReadArchive(bytes.NewReader(archive(t, "acme-site-0a1b2c3/", files)), true)
	if err != nil {
		t.Fatal(err)
	}
	wantFiles := []string{"README.md", "apps/big/package.json", "apps/web/next.config.mjs", "apps/web/package.json", "package.json", "packages/ui/button.tsx", "packages/ui/package.json", "apps/web/README.md"}
	if !slices.Equal(repo.Files, wantFiles) {
		t.Fatalf("files %q", repo.Files)
	}
	if got := slices.Sorted(maps.Keys(repo.Manifests)); !slices.Equal(got, []string{"apps/web/package.json", "package.json", "packages/ui/package.json"}) {
		t.Fatalf("manifests %q", got)
	}
	if got := repo.Workspace().Uses("apps/web"); !slices.Equal(got, []string{"packages/ui"}) {
		t.Fatalf("web uses %q", got)
	}
	folders, _ := repo.Folders()
	if len(folders) != 6 || folders[3] != (Folder{"apps/web", "Next.js", true}) {
		t.Fatalf("folders:\n%s", list(folders))
	}

	// An upload from pier deploy has no folder around it.
	plain, err := ReadArchive(bytes.NewReader(archive(t, "", workspaceFilesForTest)), false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(plain.Files, "apps/web/package.json") || len(plain.Manifests) != 3 {
		t.Fatalf("upload files %q, manifests %d", plain.Files, len(plain.Manifests))
	}
	if _, err := ReadArchive(strings.NewReader("not an archive"), true); err == nil {
		t.Fatal("read something that isn't an archive")
	}
}

func TestReadList(t *testing.T) {
	var asked []string
	read := func(file string) ([]byte, error) {
		asked = append(asked, file)
		return []byte(workspaceFilesForTest[file]), nil
	}
	files := slices.Sorted(maps.Keys(workspaceFilesForTest))
	files = append(files, "node_modules/x/package.json")
	repo, err := ReadList(files, read)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(asked, []string{"apps/web/package.json", "package.json", "packages/ui/package.json"}) {
		t.Fatalf("read %q", asked)
	}
	if got := repo.Workspace().Uses("apps/web"); !slices.Equal(got, []string{"packages/ui"}) {
		t.Fatalf("web uses %q", got)
	}
	if _, err := ReadList(files, func(string) ([]byte, error) { return nil, errors.New("git show failed") }); err == nil {
		t.Fatal("a failed read was left out")
	}
	if repo, err := ReadList(files, nil); err != nil || len(repo.Manifests) != 0 || len(repo.Files) != len(files) {
		t.Fatalf("without read: %+v, %v", repo, err)
	}
}
