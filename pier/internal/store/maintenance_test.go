package store

import (
	"context"
	"slices"
	"testing"
	"time"
)

func TestPreviewCleanupAndRetry(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	if err := s.EnsureProject(ctx, "docs"); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-31 * 24 * time.Hour)
	for _, spec := range []struct {
		id, target, status string
		at                 time.Time
	}{
		{"aaaaaa", TargetPreview, Ready, old}, {"bbbbbb", TargetPreview, Ready, old}, {"cccccc", TargetPreview, Ready, old},
		{"dddddd", TargetPreview, Ready, time.Now()}, {"eeeeee", TargetProduction, Ready, old}, {"ffffff", TargetPreview, Building, old},
	} {
		if err := s.CreateDeployment(ctx, Deployment{ID: spec.id, Project: "docs", Target: spec.target, Status: spec.status, CreatedAt: spec.at}); err != nil {
			t.Fatal(err)
		}
	}
	for _, a := range []Alias{
		{Kind: KindBranch, Host: "feat--docs.localhost", DeploymentID: "bbbbbb"},
		{Kind: KindProduction, Host: "docs.localhost", DeploymentID: "cccccc"},
		{Kind: KindProduction, Host: "docs.localhost", DeploymentID: "eeeeee"},
	} {
		a.Project = "docs"
		if err := s.SetAlias(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		n, err := s.CleanupCount(ctx)
		if n != 1 || err != nil {
			t.Fatalf("count %d, %v", n, err)
		}
		if err := s.QueuePreviewCleanup(ctx); err != nil {
			t.Fatal(err)
		}
		ids, err := s.CleanupFiles(ctx)
		if !slices.Equal(ids, []string{"aaaaaa"}) || err != nil {
			t.Fatalf("queued %v, %v", ids, err)
		}
	}
	if err := s.CreateDeployment(ctx, Deployment{ID: "aaaaaa", Project: "docs"}); err == nil {
		t.Fatal("reused ID before cleanup finished")
	}
	if err := s.FinishCleanup(ctx, "aaaaaa"); err != nil {
		t.Fatal(err)
	}
	n, err := s.CleanupCount(ctx)
	if n != 0 || err != nil {
		t.Fatalf("count %d after cleanup, %v", n, err)
	}
	ds, err := s.Deployments(ctx, "docs")
	if err != nil || len(ds) != 5 {
		t.Fatalf("lost protected history: %d, %v", len(ds), err)
	}
}

func TestSharedLinksAndProjectOverrides(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	for _, name := range []string{"blog", "docs"} {
		if err := s.EnsureProject(ctx, name); err != nil {
			t.Fatal(err)
		}
	}
	v := SharedVariable{Name: "GREETING", Value: "sealed", Projects: []string{"blog", "docs"}, Production: true, Preview: true}
	if err := s.SaveShared(ctx, v, true, false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetVariable(ctx, Variable{Name: v.Name, Project: "blog", Scope: TargetProduction, Value: "own"}); err != nil {
		t.Fatal(err)
	}
	check := func(p, scope, want string) {
		t.Helper()
		rows, err := s.BuildVariables(ctx, p, scope)
		if err != nil {
			t.Fatal(err)
		}
		if want == "" && len(rows) == 0 {
			return
		}
		if len(rows) != 1 || rows[0].Value != want {
			t.Fatalf("%s/%s = %v", p, scope, rows)
		}
	}
	check("blog", TargetProduction, "own")
	check("blog", TargetPreview, "sealed")
	check("docs", TargetProduction, "sealed")
	// Edit links without replacing the original secret.
	v.Value = ""
	v.Projects = []string{"docs"}
	if err := s.SaveShared(ctx, v, false, false); err != nil {
		t.Fatal(err)
	}
	check("blog", TargetPreview, "")
	check("docs", TargetPreview, "sealed")
	v.Projects = []string{"missing"}
	if err := s.SaveShared(ctx, v, false, false); err == nil {
		t.Fatal("accepted a missing project")
	}
	check("docs", TargetPreview, "sealed")
	if err := s.SaveShared(ctx, v, false, true); err != nil {
		t.Fatal(err)
	}
	check("docs", TargetPreview, "")
	check("blog", TargetProduction, "own")
}

func TestResourceHistoryBucketsAndRetention(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	base := time.Now().Truncate(time.Hour)
	for _, p := range []ServerSample{
		{At: base.Add(-8 * 24 * time.Hour).UnixMilli(), CPU: 99},
		{At: base.Add(-time.Minute).UnixMilli(), CPU: 25},
		{At: base.UnixMilli(), CPU: 40},
		{At: base.Add(time.Minute).UnixMilli(), CPU: 80},
	} {
		if err := s.RecordSample(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	history, err := s.Samples(ctx, 0, time.Hour)
	if err != nil || len(history) != 2 || history[0].CPU != 25 || history[1].CPU != 60 {
		t.Fatalf("unexpected history %v, %v", history, err)
	}
}
