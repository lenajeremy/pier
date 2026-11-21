package monorepo

import (
	"encoding/json"
	"path"
	"slices"
	"strings"
)

// Repo is what a reader collects from a repository: the path of every file,
// and the manifests that say how its workspace fits together.
type Repo struct {
	Files     []string          // every file, by its path in the repository
	Manifests map[string][]byte // each package.json and pnpm-workspace.yaml, by path
}

// MaxManifest is the largest manifest a reader keeps. One larger is left
// out, as nothing real comes close.
const MaxManifest = 1 << 20

// manifest reports whether a reader keeps a file's contents.
func manifest(file string) bool {
	_, name := split(file)
	return (name == "package.json" || name == "pnpm-workspace.yaml") && !skipped(file)
}

// Folders lists the repository's folders, as Folders does.
func (r *Repo) Folders() ([]Folder, bool) { return Folders(r.Files) }

type packageJSON struct {
	Name                 string            `json:"name"`
	Workspaces           json.RawMessage   `json:"workspaces"`
	Dependencies         map[string]string `json:"dependencies"`
	DevDependencies      map[string]string `json:"devDependencies"`
	PeerDependencies     map[string]string `json:"peerDependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
}

// Workspace is a repository's workspace: its packages by name, and what
// each folder's package.json depends on.
type Workspace struct {
	members  map[string]string      // a workspace package's name -> its folder
	packages map[string]packageJSON // a folder -> its package.json
}

// Workspace reads the workspace that the root package.json (npm, yarn and
// bun) and pnpm-workspace.yaml name. A repository without one has no
// members, so nothing uses anything.
func (r *Repo) Workspace() *Workspace {
	w := &Workspace{members: map[string]string{}, packages: map[string]packageJSON{}}
	for file, raw := range r.Manifests {
		file, _ = clean(file)
		dir, name := split(file)
		var pkg packageJSON
		if name == "package.json" && json.Unmarshal(raw, &pkg) == nil {
			w.packages[dir] = pkg
		}
	}
	var patterns []string
	if root, ok := w.packages[""]; ok {
		patterns = append(patterns, workspacePatterns(root.Workspaces)...)
	}
	if raw, ok := r.Manifests["pnpm-workspace.yaml"]; ok {
		patterns = append(patterns, pnpmPackages(string(raw))...)
	}
	var include, exclude [][]string
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		list := &include
		if rest, ok := strings.CutPrefix(p, "!"); ok {
			p, list = rest, &exclude
		}
		if p = cleanDir(p); p != "" {
			*list = append(*list, strings.Split(p, "/"))
		}
	}
	for dir, pkg := range w.packages {
		if dir == "" || pkg.Name == "" {
			continue
		}
		segs := strings.Split(dir, "/")
		matches := func(pattern []string) bool { return glob(pattern, segs) }
		if slices.ContainsFunc(include, matches) && !slices.ContainsFunc(exclude, matches) {
			w.members[pkg.Name] = dir
		}
	}
	return w
}

// Uses lists the workspace folders that the package at root depends on,
// and what those depend on in turn, sorted. Every kind of dependency counts,
// since any of them can change what a build makes.
func (w *Workspace) Uses(root string) []string {
	root = cleanDir(root)
	seen := map[string]bool{root: true}
	queue := []string{root}
	var uses []string
	for len(queue) > 0 {
		pkg := w.packages[queue[0]]
		queue = queue[1:]
		for _, deps := range []map[string]string{pkg.Dependencies, pkg.DevDependencies, pkg.PeerDependencies, pkg.OptionalDependencies} {
			for name := range deps {
				if dir, ok := w.members[name]; ok && !seen[dir] {
					seen[dir] = true
					uses = append(uses, dir)
					queue = append(queue, dir)
				}
			}
		}
	}
	slices.Sort(uses)
	return uses
}

// workspacePatterns reads package.json's workspaces, which is a list of
// patterns or, for yarn, {"packages": [...]}.
func workspacePatterns(raw json.RawMessage) []string {
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	var yarn struct{ Packages []string }
	json.Unmarshal(raw, &yarn)
	return yarn.Packages
}

// pnpmPackages reads the packages list of a pnpm-workspace.yaml. It knows
// only the shapes that file takes in practice, a block list or a flow list,
// rather than all of YAML.
func pnpmPackages(yaml string) []string {
	var out []string
	in := false
	for line := range strings.Lines(yaml) {
		line = strings.TrimRight(line, "\r\n")
		if i := strings.Index(line, " #"); i >= 0 {
			line = line[:i]
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !in {
			if rest, ok := strings.CutPrefix(line, "packages:"); ok {
				rest = strings.TrimSpace(rest)
				if flow, ok := strings.CutPrefix(rest, "["); ok {
					flow, _, _ = strings.Cut(flow, "]")
					for item := range strings.SplitSeq(flow, ",") {
						if item = unquote(item); item != "" {
							out = append(out, item)
						}
					}
					return out
				}
				in = true
			}
			continue
		}
		item, ok := strings.CutPrefix(trimmed, "-")
		if !ok {
			// The next key at the top ends the list.
			if line[0] != ' ' && line[0] != '\t' {
				break
			}
			continue
		}
		if item = unquote(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '\'' || s[0] == '"') && s[len(s)-1] == s[0] {
		s = s[1 : len(s)-1]
	}
	return strings.TrimSpace(s)
}

// glob matches a folder's segments against a workspace pattern's, where *
// matches within one segment and ** matches any number of them.
func glob(pattern, dir []string) bool {
	if len(pattern) == 0 {
		return len(dir) == 0
	}
	if pattern[0] == "**" {
		for i := 0; i <= len(dir); i++ {
			if glob(pattern[1:], dir[i:]) {
				return true
			}
		}
		return false
	}
	if len(dir) == 0 {
		return false
	}
	if ok, _ := path.Match(pattern[0], dir[0]); !ok {
		return false
	}
	return glob(pattern[1:], dir[1:])
}
