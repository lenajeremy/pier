package build

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckTreeAfterExport(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{"public/index.html": "hello", "private.txt": "outside the published folder"})
	public := filepath.Join(dir, "public")
	if err := os.Symlink("index.html", filepath.Join(public, "home.html")); err != nil {
		t.Fatal(err)
	}
	if err := CheckTree(public); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../private.txt", filepath.Join(public, "other")); err != nil {
		t.Fatal(err)
	}
	if err := CheckTree(dir); err != nil {
		t.Fatal(err)
	}
	if err := CheckTree(public); err == nil {
		t.Fatal("allowed link outside the immutable deployment")
	}
}
