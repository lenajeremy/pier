package board

import (
	"reflect"
	"testing"

	"pier/internal/store"
)

const domain = "pier.localhost"

func TestRows(t *testing.T) {
	aliases := []store.Alias{
		{Host: "feat-search--blog.pier.localhost", Project: "blog", Kind: store.KindBranch, Branch: "feat-search", DeploymentID: "c19e77"},
		{Host: "blog.pier.localhost", Project: "blog", Kind: store.KindProduction, Branch: "main", DeploymentID: "8f3a2c"},
		{Host: "docs.pier.localhost", Project: "docs", Kind: store.KindProduction, Branch: "main", DeploymentID: "2b71e0"},
	}
	latest := []store.Deployment{
		{ID: "8f3a2c", Project: "blog", Branch: "main", Status: store.Ready},
		{ID: "d00d11", Project: "blog", Branch: "feat-search", Status: store.Building}, // a new push to a branch with an address
		{ID: "e04b91", Project: "blog", Branch: "fix-rss", Status: store.Failed},       // a first push that failed
		{ID: "a7e2f0", Project: "blog", Branch: "feat-tags", Status: store.Building},   // a first push, building
		{ID: "2b71e0", Project: "docs", Branch: "main", Status: store.Ready},
	}
	got := Rows(aliases, latest, domain, "main")
	want := []Row{
		{Address: "blog.pier.localhost", Project: "blog", Kind: store.KindProduction, Deployment: "8f3a2c", Branch: "main", Status: Live},
		{Address: "feat-search--blog.pier.localhost", Project: "blog", Kind: store.KindBranch, Deployment: "d00d11", Branch: "feat-search", Status: Building},
		{Address: "feat-tags--blog.pier.localhost", Project: "blog", Kind: store.KindBranch, Deployment: "a7e2f0", Branch: "feat-tags", Status: Building},
		{Address: "fix-rss--blog.pier.localhost", Project: "blog", Kind: store.KindBranch, Deployment: "e04b91", Branch: "fix-rss", Status: Failed},
		{Address: "docs.pier.localhost", Project: "docs", Kind: store.KindProduction, Deployment: "2b71e0", Branch: "main", Status: Live},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got\n%+v\nwant\n%+v", got, want)
	}
}

func TestFirstPushToProduction(t *testing.T) {
	got := Rows(nil, []store.Deployment{{ID: "9a0f31", Project: "api", Branch: "main", Status: store.Building}}, domain, "main")
	if len(got) != 1 || got[0].Address != "api.pier.localhost" || got[0].Status != Building || got[0].Kind != store.KindProduction {
		t.Fatalf("got %+v", got)
	}
}
