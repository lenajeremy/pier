package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "pier.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestDeploymentsAndAliases(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	if err := s.EnsureProject(ctx, "blog"); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureProject(ctx, "blog"); err != nil {
		t.Fatal("EnsureProject should be idempotent:", err)
	}

	t0 := time.Now().Add(-time.Hour)
	for i, d := range []Deployment{
		{ID: "41d0e9", Branch: "main", Commit: "aaa", Message: "Fix RSS dates", CreatedAt: t0},
		{ID: "8f3a2c", Branch: "main", Commit: "bbb", Message: "Add search page", CreatedAt: t0.Add(time.Minute)},
		{ID: "c19e77", Branch: "feat-search", Commit: "ccc", Message: "Search results page", CreatedAt: t0.Add(2 * time.Minute)},
	} {
		d.Project, d.Status = "blog", Building
		if err := s.CreateDeployment(ctx, d); err != nil {
			t.Fatal(i, err)
		}
	}
	if err := s.FinishDeployment(ctx, Deployment{ID: "8f3a2c", Status: Ready, Framework: "astro 4.16", Files: 214, Bytes: 1_300_000, BuildMS: 9800}); err != nil {
		t.Fatal(err)
	}

	d, err := s.Deployment(ctx, "8f3a2c")
	if err != nil || d.Status != Ready || d.Files != 214 || d.Message != "Add search page" {
		t.Fatalf("got %+v, %v", d, err)
	}
	if _, err := s.Deployment(ctx, "ffffff"); err != ErrNotFound {
		t.Fatalf("missing deployment: %v", err)
	}

	all, _ := s.Deployments(ctx, "blog")
	if len(all) != 3 || all[0].ID != "c19e77" {
		t.Fatalf("Deployments not newest first: %+v", all)
	}
	latest, _ := s.LatestPerBranch(ctx)
	if len(latest) != 2 || latest[0].ID != "c19e77" || latest[1].ID != "8f3a2c" {
		t.Fatalf("LatestPerBranch: %+v", latest)
	}
	ready, _ := s.ReadyDeployments(ctx)
	if len(ready) != 1 || ready[0].ID != "8f3a2c" {
		t.Fatalf("ReadyDeployments: %+v", ready)
	}

	a := Alias{Host: "blog.pier.localhost", Project: "blog", Kind: KindProduction, Branch: "main", DeploymentID: "8f3a2c"}
	if err := s.SetAlias(ctx, a); err != nil {
		t.Fatal(err)
	}
	a.DeploymentID = "41d0e9" // a rollback: same host, older deployment
	if err := s.SetAlias(ctx, a); err != nil {
		t.Fatal(err)
	}
	aliases, _ := s.Aliases(ctx)
	if len(aliases) != 1 || aliases[0].DeploymentID != "41d0e9" {
		t.Fatalf("Aliases: %+v", aliases)
	}
}
