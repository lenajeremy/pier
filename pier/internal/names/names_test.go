package names

import (
	"strings"
	"testing"
)

func TestValidProject(t *testing.T) {
	for name, want := range map[string]bool{
		"blog": true, "status-page": true, "a": true, "x1": true,
		"": false, "Blog": false, "-blog": false, "blog-": false, "my--blog": false,
		"blog.site": false, "blog_site": false, strings.Repeat("a", 40): false,
	} {
		if got := ValidProject(name); got != want {
			t.Errorf("ValidProject(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestBranchSlug(t *testing.T) {
	for branch, want := range map[string]string{
		"main":              "main",
		"feat-search":       "feat-search",
		"feat/Search_Page":  "feat-search-page",
		"fix--rss":          "fix-rss",
		"--weird//branch--": "weird-branch",
		"///":               "branch",
	} {
		if got := BranchSlug(branch, "blog"); got != want {
			t.Errorf("BranchSlug(%q) = %q, want %q", branch, got, want)
		}
	}
}

func TestBranchFitsOneLabel(t *testing.T) {
	host := Branch("blog", strings.Repeat("long-branch-name-", 8), "pier.example.com")
	label := strings.SplitN(host, ".", 2)[0]
	if len(label) > 63 {
		t.Fatalf("label is %d characters: %q", len(label), label)
	}
	if !strings.HasSuffix(label, "--blog") {
		t.Fatalf("label lost its project: %q", label)
	}
}

func TestAddresses(t *testing.T) {
	d := "pier.example.com"
	if got := Production("blog", d); got != "blog.pier.example.com" {
		t.Error(got)
	}
	if got := Branch("blog", "feat-search", d); got != "feat-search--blog.pier.example.com" {
		t.Error(got)
	}
	if got := Deployment("blog", "c19e77", d); got != "blog-c19e77.pier.example.com" {
		t.Error(got)
	}
	if id := NewID(); len(id) != 6 {
		t.Errorf("NewID() = %q", id)
	}
}
