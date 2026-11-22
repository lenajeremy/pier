package api

import (
	"net/http"
	"sort"
	"strings"

	"pier/internal/deploy"
	"pier/internal/store"
)

// hiddenValue stands in for a variable's value in a log a viewer reads.
const hiddenValue = "••••••"

// plainWords name environments rather than hold secrets. Hiding them would
// blank out half of every log ("building for production") and hide nothing.
var plainWords = map[string]bool{
	"production": true, "preview": true, "development": true, "staging": true,
}

// valueHider hides a project's variable values in the logs a viewer reads.
// Pier never writes a value to a log, but a build or an app can print one.
// For members and the owner it changes nothing. Values shorter than six
// characters are left alone, so "true" and "1" don't blank out a log.
func (a *API) valueHider(r *http.Request, project string) (func(string) string, error) {
	keep := func(s string) string { return s }
	if userFrom(r.Context()).Role != store.RoleViewer || a.Deployer.Secrets == nil {
		return keep, nil
	}
	seen := map[string]bool{}
	var values []string
	for _, scope := range []string{store.TargetProduction, store.TargetPreview} {
		vars, err := a.Store.BuildVariables(r.Context(), project, scope)
		if err != nil {
			return nil, err
		}
		for _, v := range vars {
			binding := deploy.SealedFor(project, scope, v.Name)
			if v.Project == "" {
				binding = deploy.SharedBinding(v.Name)
			}
			value, err := a.Deployer.Secrets.Open(v.Value, binding)
			if err != nil {
				continue // a value that can't be opened can't be printed either
			}
			// A log line holds at most one line of a value.
			for _, line := range strings.Split(value, "\n") {
				line = strings.TrimSpace(line)
				if len(line) >= 6 && !plainWords[strings.ToLower(line)] && !seen[line] {
					seen[line] = true
					values = append(values, line)
				}
			}
		}
	}
	if len(values) == 0 {
		return keep, nil
	}
	// Longer values first, so one that contains another is hidden whole.
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	pairs := make([]string, 0, 2*len(values))
	for _, v := range values {
		pairs = append(pairs, v, hiddenValue)
	}
	return strings.NewReplacer(pairs...).Replace, nil
}
