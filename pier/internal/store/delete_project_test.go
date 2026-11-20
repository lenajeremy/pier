package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestDeleteProject(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "pier.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if err := st.CreateProject(ctx, Project{Name: "demo"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetVariable(ctx, Variable{Project: "demo", Scope: TargetProduction, Name: "API_KEY", Value: "sealed"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveShared(ctx, SharedVariable{Name: "SHARED", Value: "sealed", Production: true, Projects: []string{"demo"}}, true, false); err != nil {
		t.Fatal(err)
	}
	if err := st.AddDomain(ctx, "demo", "demo.example.com", "www.demo.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateDeployment(ctx, Deployment{ID: "aaaaaa", Project: "demo", Branch: "main", Commit: "1111111", Status: Building, Target: TargetProduction, Source: SourceCLI}); err != nil {
		t.Fatal(err)
	}
	if err := st.TrackApp(ctx, "aaaaaa", "pier-app-demo1"); err != nil {
		t.Fatal(err)
	}

	// Refuse while building.
	if err := st.DeleteProject(ctx, "demo"); !errors.Is(err, ErrBuilding) {
		t.Fatalf("expected ErrBuilding while active build exists, got %v", err)
	}

	// Finish deployment, attach alias, then delete cleanly.
	if err := st.FinishDeployment(ctx, Deployment{ID: "aaaaaa", Status: Ready}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAlias(ctx, Alias{Host: "demo.pier.localhost", Project: "demo", Kind: KindProduction, Branch: "main", DeploymentID: "aaaaaa"}); err != nil {
		t.Fatal(err)
	}

	if err := st.DeleteProject(ctx, "demo"); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if _, err := st.Project(ctx, "demo"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
	files, err := st.CleanupFiles(ctx)
	if err != nil || len(files) != 1 || files[0] != "aaaaaa" {
		t.Fatalf("cleanup_files = %v, %v; want [aaaaaa]", files, err)
	}
	apps, err := st.UnusedApps(ctx)
	if err != nil || len(apps) != 1 || apps[0] != "pier-app-demo1" {
		t.Fatalf("unused apps = %v, %v; want [pier-app-demo1]", apps, err)
	}
	if err := st.DeleteProject(ctx, "demo"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound on second delete, got %v", err)
	}
}
