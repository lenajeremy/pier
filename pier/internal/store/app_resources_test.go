package store

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Thirty pushes must not leave thirty processes: only what an address serves,
// what a rollback needs at once, and the newest of each branch stay running.
func TestIdleAppsAreTheSupersededOnes(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	for _, p := range []string{"shop", "docs"} {
		if err := s.EnsureProject(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now().Add(-time.Hour)
	add := func(id, project, branch, target, status string, minute int) {
		t.Helper()
		dep := Deployment{ID: id, Project: project, Branch: branch, Target: target, Status: Building, CreatedAt: start.Add(time.Duration(minute) * time.Minute)}
		if err := s.CreateDeployment(ctx, dep); err != nil {
			t.Fatal(err)
		}
		if err := s.TrackApp(ctx, id, "app-"+id); err != nil {
			t.Fatal(err)
		}
		if status != Building {
			if err := s.FinishDeployment(ctx, Deployment{ID: id, Status: status}); err != nil {
				t.Fatal(err)
			}
		}
	}
	point := func(kind, host, project, id string) {
		t.Helper()
		if err := s.SetAlias(ctx, Alias{Kind: kind, Host: host, Project: project, DeploymentID: id}); err != nil {
			t.Fatal(err)
		}
	}
	idle := func(project string, want ...string) {
		t.Helper()
		got, err := s.IdleApps(ctx, project)
		if err != nil || !slices.Equal(got, want) {
			t.Fatalf("idle in %q = %v, %v; want %v", project, got, err, want)
		}
	}

	// Four production pushes, the last of which serves.
	for i, id := range []string{"a00001", "a00002", "a00003", "a00004"} {
		add(id, "shop", "main", TargetProduction, Ready, i)
	}
	add("a0fa11", "shop", "main", TargetProduction, Failed, 5)
	add("a0b111", "shop", "main", TargetProduction, Building, 6)
	point(KindProduction, "shop.localhost", "shop", "a00004")
	// A pushed branch follows its newest build; pier deploy's previews have
	// no address but their own.
	add("b00001", "shop", "feat", TargetPreview, Ready, 1)
	add("b00002", "shop", "feat", TargetPreview, Ready, 2)
	point(KindBranch, "feat--shop.localhost", "shop", "b00002")
	add("c00001", "shop", "try", TargetPreview, Ready, 1)
	add("c00002", "shop", "try", TargetPreview, Ready, 2)
	// Another project's only deployment stays out of it.
	add("d00001", "docs", "main", TargetProduction, Ready, 1)
	point(KindProduction, "docs.localhost", "docs", "d00001")

	// a00004 serves and a00003 is where a rollback would go.
	idle("shop", "app-a00001", "app-a00002", "app-b00001", "app-c00001")
	idle("docs")
	idle("", "app-a00001", "app-a00002", "app-b00001", "app-c00001")

	// What may sleep: the superseded at once, and the previews that only a
	// branch's address or their own serves once nobody visits them.
	sleepers := func(project string, want ...string) {
		t.Helper()
		list, err := s.Sleepers(ctx, project)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, z := range list {
			kind := "preview"
			if z.Superseded {
				kind = "superseded"
			}
			got = append(got, z.Name+" "+kind)
			if z.Deployment != strings.TrimPrefix(z.Name, "app-") || z.Ready.Before(start.Add(-time.Second)) || z.Ready.After(time.Now()) {
				t.Errorf("sleeper %+v", z)
			}
		}
		if !slices.Equal(got, want) {
			t.Fatalf("sleepers in %q = %v, want %v", project, got, want)
		}
	}
	sleepers("shop", "app-a00001 superseded", "app-a00002 superseded", "app-b00001 superseded", "app-b00002 preview", "app-c00001 superseded", "app-c00002 preview")
	sleepers("docs")
	// A preview production was promoted to is production's.
	add("d00002", "docs", "try", TargetPreview, Ready, 2)
	point(KindProduction, "docs.localhost", "docs", "d00002")
	sleepers("docs")

	// Rolled back to a00002: it serves, a00004 is where undo goes, and
	// a00001 is where the next rollback would go.
	point(KindProduction, "shop.localhost", "shop", "a00002")
	if err := s.SetRolledBack(ctx, "shop", "a00004"); err != nil {
		t.Fatal(err)
	}
	idle("shop", "app-a00003", "app-b00001", "app-c00001")

	// A build held back by the rollback is the newest of its branch, so it
	// waits running, and the one production was rolled back from stays too.
	add("a00005", "shop", "main", TargetProduction, Ready, 7)
	idle("shop", "app-a00003", "app-b00001", "app-c00001")
	if err := s.SetRolledBack(ctx, "shop", ""); err != nil {
		t.Fatal(err)
	}
	idle("shop", "app-a00003", "app-a00004", "app-b00001", "app-c00001")
}

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
