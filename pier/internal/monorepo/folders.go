// Package monorepo reads how a repository is laid out: its folders and what
// each looks like, which workspace packages a folder uses, and whether a set
// of changed files touches a project built from one folder. It needs only
// file names and package manifests, so a GitHub tree, a git commit and an
// uploaded archive are all read the same way.
package monorepo

import (
	"cmp"
	"path"
	"slices"
	"strings"
)

// Folder is one folder of a repository.
type Folder struct {
	Path string `json:"path"` // relative to the repository; "" is its root
	Kind string `json:"kind"` // what it looks like, like "Next.js" or "Go"; "" when nothing says
	App  bool   `json:"app"`  // likely deployable on its own, so worth suggesting as a root directory
}

// MaxFolders is how many folders Folders lists. A repository with more is
// listed in part, and Folders says so.
const MaxFolders = 5000

// skippedDirs hold what was installed or built, never a project's source.
var skippedDirs = map[string]bool{"node_modules": true, "vendor": true, "dist": true, "build": true, ".next": true, "target": true}

// kinds name what a folder looks like from the files directly in it, most
// specific first. A SvelteKit or Remix app has a vite.config too, so the
// frameworks built on Vite come before it, and nearly every JavaScript
// folder has a package.json, so Node comes late.
var kinds = []struct {
	kind  string
	files []string // exact names, or a prefix ending in "." for name.config.*
}{
	{"Next.js", []string{"next.config."}},
	{"Astro", []string{"astro.config."}},
	{"Nuxt", []string{"nuxt.config."}},
	{"SvelteKit", []string{"svelte.config."}},
	{"Remix", []string{"remix.config."}},
	{"Docusaurus", []string{"docusaurus.config."}},
	{"Vite", []string{"vite.config."}},
	{"Dockerfile", []string{"Dockerfile"}},
	{"Go", []string{"go.mod"}},
	{"Rust", []string{"Cargo.toml"}},
	{"Python", []string{"pyproject.toml", "requirements.txt"}},
	{"Ruby", []string{"Gemfile"}},
	{"PHP", []string{"composer.json"}},
	{"Deno", []string{"deno.json", "deno.jsonc"}},
	{"Node", []string{"package.json"}},
	{"Static", []string{"index.html"}},
}

// workspaceFiles mark a repository's root as the root of a workspace, which
// is how it is best described even when it also has a package.json.
var workspaceFiles = []string{"pnpm-workspace.yaml", "turbo.json", "nx.json", "lerna.json"}

// Folders lists the folders of a repository from the paths of its files,
// sorted so each folder comes right before what is in it. The root is always
// first. Hidden folders and files, and folders of installed or built files,
// are left out. More than MaxFolders are cut to the first MaxFolders, and
// truncated says so.
func Folders(files []string) (folders []Folder, truncated bool) {
	names := map[string][]string{"": nil} // a folder -> the files directly in it
	for _, f := range files {
		f, ok := clean(f)
		if !ok || skipped(f) {
			continue
		}
		dir, name := split(f)
		names[dir] = append(names[dir], name)
		// A folder is only added with all of its parents, so the first one
		// already there has the rest.
		for d := dir; d != ""; {
			d, _ = split(d)
			if _, ok := names[d]; ok {
				break
			}
			names[d] = nil
		}
	}
	paths := make([]string, 0, len(names))
	for p := range names {
		paths = append(paths, p)
	}
	slices.SortFunc(paths, comparePaths)
	if len(paths) > MaxFolders {
		paths, truncated = paths[:MaxFolders], true
	}
	apps := map[string]bool{}
	folders = make([]Folder, 0, len(paths))
	for _, p := range paths {
		f := Folder{Path: p, Kind: kindOf(p, names[p])}
		f.App = isApp(f, apps)
		if f.App {
			apps[p] = true
		}
		folders = append(folders, f)
	}
	return folders, truncated
}

func kindOf(dir string, names []string) string {
	has := func(want string) bool {
		return slices.ContainsFunc(names, func(n string) bool {
			if strings.HasSuffix(want, ".") {
				return strings.HasPrefix(n, want) && len(n) > len(want)
			}
			return n == want
		})
	}
	if dir == "" && slices.ContainsFunc(workspaceFiles, has) {
		return "Workspace"
	}
	for _, k := range kinds {
		if slices.ContainsFunc(k.files, has) {
			return k.kind
		}
	}
	return ""
}

// isApp says whether a folder is likely deployable on its own. The root is
// whatever the whole repository is, so it is never suggested. A Node package
// under packages/ or libs/ is a library by convention, and a page inside
// another app, like a Create React App's public/index.html, is part of it.
func isApp(f Folder, apps map[string]bool) bool {
	if f.Path == "" || f.Kind == "" {
		return false
	}
	segs := strings.Split(f.Path, "/")
	parents := segs[:len(segs)-1]
	switch f.Kind {
	case "Node":
		return !slices.Contains(parents, "packages") && !slices.Contains(parents, "libs")
	case "Static":
		for i := range parents {
			if apps[strings.Join(parents[:i+1], "/")] {
				return false
			}
		}
	}
	return true
}

// clean makes a path relative to the repository, with forward slashes and
// nothing like "./", "../" or "a//b" in it. The repository itself isn't a
// file in it.
func clean(p string) (string, bool) {
	p = strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(p, "\\", "/")), "/")
	return p, p != ""
}

// cleanDir is clean for a folder, where "" and "." both mean the root.
func cleanDir(dir string) string {
	dir, _ = clean(dir)
	return dir
}

// skipped reports whether a file is hidden, or in a hidden folder or one of
// installed or built files.
func skipped(file string) bool {
	segs := strings.Split(file, "/")
	for i, s := range segs {
		if strings.HasPrefix(s, ".") || i < len(segs)-1 && skippedDirs[s] {
			return true
		}
	}
	return false
}

// split returns a path's folder ("" for the root) and its last name.
func split(p string) (dir, name string) {
	i := strings.LastIndexByte(p, '/')
	if i < 0 {
		return "", p
	}
	return p[:i], p[i+1:]
}

// comparePaths orders paths so a folder's contents come right after it:
// apps/web, apps/web/src, apps/web-admin. A plain string order would put
// apps/web-admin between the other two, since "-" sorts before "/".
func comparePaths(a, b string) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] == b[i] {
			continue
		}
		if a[i] == '/' {
			return -1
		}
		if b[i] == '/' {
			return 1
		}
		return cmp.Compare(a[i], b[i])
	}
	return cmp.Compare(len(a), len(b))
}
