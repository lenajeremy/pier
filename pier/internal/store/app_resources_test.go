package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestAppResourcesSurviveInterruptedBuildAndPreviewCleanup(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "pier.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.EnsureProject(ctx, "app"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"111111", "222222"} {
		dep := Deployment{ID: id, Project: "app", Status: Building, Target: TargetPreview, CreatedAt: time.Now().Add(-32 * 24 * time.Hour)}
		if err := s.CreateDeployment(ctx, dep); err != nil {
			t.Fatal(err)
		}
		if err := s.TrackApp(ctx, id, "container-"+id); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.FinishDeployment(ctx, Deployment{ID: "222222", Status: Ready}); err != nil {
		t.Fatal(err)
	}
	if err := s.InterruptAppBuilds(ctx); err != nil {
		t.Fatal(err)
	}
	ready, err := s.ReadyDeployments(ctx)
	if err != nil || len(ready) != 1 || ready[0].Runtime != "container-222222" {
		t.Fatalf("ready %v, %v", ready, err)
	}
	unused, err := s.UnusedApps(ctx)
	if err != nil || len(unused) != 1 || unused[0] != "container-111111" {
		t.Fatalf("interrupted %v, %v", unused, err)
	}
	if err := s.QueuePreviewCleanup(ctx); err != nil {
		t.Fatal(err)
	}
	unused, err = s.UnusedApps(ctx)
	if err != nil || len(unused) != 2 {
		t.Fatalf("cleanup lost Docker ownership %v, %v", unused, err)
	}
}
