package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAlwaysBuild(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	if err := s.CreateProject(ctx, Project{Name: "docs", Build: Build{Root: "apps/docs", AlwaysBuild: true}}); err != nil {
		t.Fatal(err)
	}
	if p, err := s.Project(ctx, "docs"); err != nil || !p.Build.AlwaysBuild || p.Build.Root != "apps/docs" {
		t.Fatalf("created %+v, %v", p.Build, err)
	}
	if err := s.SetProjectBuild(ctx, "docs", "", Build{Root: "apps/docs"}); err != nil {
		t.Fatal(err)
	}
	if p, err := s.Project(ctx, "docs"); err != nil || p.Build.AlwaysBuild {
		t.Fatalf("saved %+v, %v", p.Build, err)
	}
	// An import keeps what the owner chose on the import page.
	if err := s.ImportGitHub(ctx, Project{Name: "site", ProductionBranch: "main", Build: Build{Root: "web", AlwaysBuild: true}}, 7, 42, strings.Repeat("a", 40)); err != nil {
		t.Fatal(err)
	}
	if p, err := s.Project(ctx, "site"); err != nil || !p.Build.AlwaysBuild || p.Build.Root != "web" {
		t.Fatalf("imported %+v, %v", p.Build, err)
	}
}

func TestGitHubLinkAndImportJob(t *testing.T) {
	ctx := context.Background()
	s := githubStore(t)
	if repo, installation, err := s.GitHubLink(ctx, "site"); err != nil || repo != 7 || installation != 42 {
		t.Fatalf("site links to %d through %d, %v", repo, installation, err)
	}
	if err := s.EnsureProject(ctx, "cli"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.GitHubLink(ctx, "cli"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a project without a repository links with %v", err)
	}
	imported, err := s.NextGitHub(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.ImportJob(ctx, imported.ID); err != nil || !ok {
		t.Fatalf("the import job reads as %v, %v", ok, err)
	}
	if _, err := s.QueueGitHub(ctx, 7, 42, "d1", "main", strings.Repeat("b", 40), 0, false, time.Time{}); err != nil {
		t.Fatal(err)
	}
	var pushed int64
	if err := s.db.QueryRow("SELECT id FROM github_jobs WHERE delivery='d1'").Scan(&pushed); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.ImportJob(ctx, pushed); err != nil || ok {
		t.Fatalf("a push reads as an import: %v, %v", ok, err)
	}
	if _, err := s.ImportJob(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a missing job reads with %v", err)
	}
}
