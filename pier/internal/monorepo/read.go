package monorepo

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ReadArchive reads a repository from a gzipped tar: every file's path, and
// the manifests' contents. A GitHub source archive keeps every entry in one
// top-level folder named for the repository and commit, which github says
// to strip.
func ReadArchive(r io.Reader, github bool) (*Repo, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("not a gzipped tar: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	repo := &Repo{Manifests: map[string][]byte{}}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return repo, nil
		}
		if err != nil {
			return nil, err
		}
		// A link is a file of the repository too; folders follow from files.
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeSymlink {
			continue
		}
		name := strings.TrimPrefix(h.Name, "./")
		if github {
			_, rest, ok := strings.Cut(name, "/")
			if !ok {
				continue
			}
			name = rest
		}
		name, ok := clean(name)
		if !ok {
			continue
		}
		repo.Files = append(repo.Files, name)
		if h.Typeflag == tar.TypeReg && manifest(name) && h.Size <= MaxManifest {
			raw, err := io.ReadAll(io.LimitReader(tr, MaxManifest))
			if err != nil {
				return nil, err
			}
			repo.Manifests[name] = raw
		}
	}
}

// ReadList reads a repository from the paths of its files, asking read for
// each manifest's contents, as a bare git repository answers git show
// <commit>:<path>. A manifest larger than MaxManifest is left out. A nil
// read reads none, which is all that listing folders needs. A read that
// fails fails the whole, since a workspace read in part could say a package
// uses less than it does.
func ReadList(files []string, read func(file string) ([]byte, error)) (*Repo, error) {
	repo := &Repo{Manifests: map[string][]byte{}}
	for _, f := range files {
		f, ok := clean(f)
		if !ok {
			continue
		}
		repo.Files = append(repo.Files, f)
		if read == nil || !manifest(f) {
			continue
		}
		raw, err := read(f)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", f, err)
		}
		if len(raw) <= MaxManifest {
			repo.Manifests[f] = raw
		}
	}
	return repo, nil
}
