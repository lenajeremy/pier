// Package bundle packs a folder for pier deploy and unpacks it on the
// server: a gzipped tar of regular files and folders, nothing else.
package bundle

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

// Limits on what an upload may unpack to.
const (
	MaxFiles = 50_000
	MaxBytes = 2 << 30 // 2 GB unpacked
)

// skip is always left out of an upload: version control, installed
// dependencies (the build installs its own), and Pier's own link file.
var skip = map[string]bool{".git": true, "node_modules": true, ".pier": true, ".DS_Store": true}

// Files lists what to upload from dir, as slash-separated relative paths.
// In a git checkout it's every file git would keep (tracked, or new and not
// ignored); elsewhere it's every file, minus the skip list.
func Files(dir string) ([]string, error) {
	if out, err := exec.Command("git", "-C", dir, "ls-files", "-z", "--cached", "--others", "--exclude-standard").Output(); err == nil {
		var files []string
		for _, f := range strings.Split(string(out), "\x00") {
			if f == "" || skipped(f) {
				continue
			}
			if fi, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(f))); err == nil && fi.Mode().IsRegular() {
				files = append(files, f)
			}
		}
		return files, nil
	}
	var files []string
	err := filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if rel == "." {
			return nil
		}
		if skip[e.Name()] {
			if e.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if e.Type().IsRegular() {
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	return files, err
}

func skipped(rel string) bool {
	for _, part := range strings.Split(rel, "/") {
		if skip[part] {
			return true
		}
	}
	return false
}

// Pack writes files from dir to w as a gzipped tar, and returns how many
// bytes they hold.
func Pack(w io.Writer, dir string, files []string) (int64, error) {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	var total int64
	for _, f := range files {
		p := filepath.Join(dir, filepath.FromSlash(f))
		fi, err := os.Stat(p)
		if err != nil {
			return total, err
		}
		h := &tar.Header{Name: f, Mode: int64(fi.Mode().Perm()), Size: fi.Size(), ModTime: fi.ModTime(), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(h); err != nil {
			return total, err
		}
		src, err := os.Open(p)
		if err != nil {
			return total, err
		}
		n, err := io.Copy(tw, src)
		src.Close()
		if err != nil {
			return total, err
		}
		total += n
	}
	if err := tw.Close(); err != nil {
		return total, err
	}
	return total, gz.Close()
}

// ErrUnsafe means an upload tried to write outside its folder, or held
// something other than files and folders.
var ErrUnsafe = errors.New("the upload holds a path Pier won't unpack")

// Untar unpacks a gzipped tar from archive into dir. Only regular files and
// folders are written, every path must stay inside dir, and the totals are
// capped, so an upload can't reach the rest of the server.
func Untar(archive, dir string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("not a gzipped tar: %w", err)
	}
	tr := tar.NewReader(gz)
	var files int
	var total int64
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := path.Clean("/" + h.Name)[1:] // rooted clean drops any ..
		if name == "" || name != strings.TrimPrefix(path.Clean(h.Name), "./") {
			return fmt.Errorf("%w: %s", ErrUnsafe, h.Name)
		}
		target := filepath.Join(dir, filepath.FromSlash(name))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if files++; files > MaxFiles {
				return fmt.Errorf("the upload has more than %d files", MaxFiles)
			}
			if total += h.Size; total > MaxBytes {
				return fmt.Errorf("the upload unpacks to more than %d GB", MaxBytes>>30)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fs.FileMode(h.Mode)&0o755|0o644)
			if err != nil {
				return err
			}
			_, err = io.CopyN(out, tr, h.Size)
			out.Close()
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("%w: %s is a link or device", ErrUnsafe, h.Name)
		}
	}
}
