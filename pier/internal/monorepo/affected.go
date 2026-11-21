package monorepo

import (
	"slices"
	"strings"
)

// rootFiles are the files at a repository's root that change how every
// workspace project installs or builds: the root manifest and lockfiles,
// the workspace's own configuration, the tool versions and the shared
// TypeScript settings.
var rootFiles = map[string]bool{
	"package.json": true, "package-lock.json": true, "pnpm-lock.yaml": true, "yarn.lock": true,
	"bun.lockb": true, "bun.lock": true, "pnpm-workspace.yaml": true, "turbo.json": true, "nx.json": true,
	".npmrc": true, ".nvmrc": true, ".node-version": true, "tsconfig.json": true, "tsconfig.base.json": true,
}

// Affected reports whether a change to the files changed can change what a
// project built from root makes: root is the whole repository, or a changed
// file is in root, in a folder it uses, or is one of rootFiles. Folders
// contain only what is below them by whole names, so apps/web doesn't
// contain apps/website. A file renamed is in changed under both names.
func Affected(root string, uses []string, changed []string) bool {
	root = cleanDir(root)
	if root == "" {
		return true
	}
	for _, c := range changed {
		c, ok := clean(c)
		if !ok {
			continue
		}
		if rootFiles[c] || inside(c, root) || slices.ContainsFunc(uses, func(dir string) bool { return inside(c, cleanDir(dir)) }) {
			return true
		}
	}
	return false
}

// inside reports whether file is in dir, a cleaned folder.
func inside(file, dir string) bool {
	return dir == "" || file == dir || strings.HasPrefix(file, dir+"/")
}
