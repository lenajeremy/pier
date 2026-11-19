// Package board works out the rows of the board: every address on the
// server, what it serves, and what state it's in. The CLI and the dashboard
// both show these rows.
package board

import (
	"sort"

	"pier/internal/names"
	"pier/internal/store"
)

// Statuses shown on the board.
const (
	Live     = "live"     // production, serving
	Preview  = "preview"  // a branch's address, serving
	Building = "building" // a new deployment for this address is building
	Failed   = "failed"   // the address has never served: its only build failed
)

type Row struct {
	Address    string `json:"address"`
	Project    string `json:"project"`
	Kind       string `json:"kind"`
	Deployment string `json:"deployment"`
	Branch     string `json:"branch"`
	Status     string `json:"status"`
}

// Rows builds the board from the aliases and each branch's latest deployment.
// An address keeps its status while a newer build is running, except that the
// row says "building" so you can see the push landed. A branch that has
// never built successfully still gets a row, so a first push shows up at once.
func Rows(aliases []store.Alias, latest []store.Deployment, domain, productionBranch string) []Row {
	type key struct{ project, branch string }
	newest := map[key]store.Deployment{}
	for _, d := range latest {
		newest[key{d.Project, d.Branch}] = d
	}

	var rows []Row
	seen := map[string]bool{}
	add := func(r Row) {
		if !seen[r.Address] {
			seen[r.Address] = true
			rows = append(rows, r)
		}
	}

	for _, a := range aliases {
		r := Row{Address: a.Host, Project: a.Project, Kind: a.Kind, Deployment: a.DeploymentID, Branch: a.Branch, Status: Preview}
		if a.Kind == store.KindProduction {
			r.Status = Live
		}
		if d, ok := newest[key{a.Project, a.Branch}]; ok && d.Status == store.Building && d.ID != a.DeploymentID {
			r.Status, r.Deployment = Building, d.ID
		}
		add(r)
	}

	// Branches whose address doesn't exist yet: first pushes, building or failed.
	for _, d := range latest {
		if d.Status == store.Ready {
			continue
		}
		kind, host := store.KindBranch, names.Branch(d.Project, d.Branch, domain)
		if d.Branch == productionBranch {
			kind, host = store.KindProduction, names.Production(d.Project, domain)
		}
		if seen[host] {
			continue
		}
		status := Building
		if d.Status == store.Failed {
			status = Failed
		}
		add(Row{Address: host, Project: d.Project, Kind: kind, Deployment: d.ID, Branch: d.Branch, Status: status})
	}

	// Production first within each project, then branches by address.
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Project != b.Project {
			return a.Project < b.Project
		}
		if (a.Kind == store.KindProduction) != (b.Kind == store.KindProduction) {
			return a.Kind == store.KindProduction
		}
		return a.Address < b.Address
	})
	return rows
}
