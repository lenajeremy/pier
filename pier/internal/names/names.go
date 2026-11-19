// Package names turns projects, branches and deployments into addresses.
//
// Every deployment gets three kinds of address under the server's domain:
//
//	blog.pier.example.com                 production (the production branch)
//	feat-search--blog.pier.example.com    the latest deployment of a branch
//	blog-c19e77.pier.example.com          one deployment, forever
package names

import (
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"strings"
)

// dnsLabel is the longest a single DNS label may be.
const dnsLabel = 63

var projectRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,37}[a-z0-9])?$`)

// ValidProject reports whether name can be a project: lowercase letters,
// digits and single dashes, 1 to 39 characters, not starting or ending with a
// dash, and no double dash (that's the branch separator).
func ValidProject(name string) bool {
	return projectRe.MatchString(name) && !strings.Contains(name, "--")
}

// BranchSlug makes a branch name safe for a DNS label that also holds the
// project: feat/Search_Page becomes feat-search-page.
func BranchSlug(branch, project string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(branch) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	// "<branch>--<project>" has to fit in one label.
	if room := dnsLabel - 2 - len(project); len(s) > room {
		s = strings.TrimRight(s[:room], "-")
	}
	if s == "" {
		s = "branch"
	}
	return s
}

// Production is the address production is served at.
func Production(project, domain string) string { return project + "." + domain }

// Branch is the address that follows a branch's latest deployment.
func Branch(project, branch, domain string) string {
	return BranchSlug(branch, project) + "--" + project + "." + domain
}

// Deployment is the address that always serves one deployment.
func Deployment(project, id, domain string) string { return project + "-" + id + "." + domain }

// NewID returns a random deployment ID: six lowercase hex characters, like
// the short form of a commit.
func NewID() string {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
