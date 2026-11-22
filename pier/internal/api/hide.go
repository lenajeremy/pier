package api

import (
	"net/http"
	"sort"
	"strings"

	"pier/internal/store"
)

// hiddenValue stands in for a variable's value in a log a viewer reads.
const hiddenValue = "••••••"

// plainWords name environments rather than hold secrets. Hiding them would
// blank out half of every log ("building for production") and hide nothing.
var plainWords = map[string]bool{
	"production": true, "preview": true, "development": true, "staging": true,
}

// valueHider hides a project's variable values, and the CRON_SECRET Pier
// makes for its jobs, in the logs and job output a viewer reads. Pier never
// writes a value to a log, but a build, an app or a job can print one. For
// members and the owner it changes nothing. Values shorter than six
// characters are left alone, so "true" and "1" don't blank out a log.
func (a *API) valueHider(r *http.Request, project string) (func(string) string, error) {
	keep := func(s string) string { return s }
	if userFrom(r.Context()).Role != store.RoleViewer {
		return keep, nil
	}
	all, err := a.Deployer.SecretValues(r.Context(), project)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var values []string
	for _, value := range all {
		// A log line holds at most one line of a value.
		for _, line := range strings.Split(value, "\n") {
			line = strings.TrimSpace(line)
			if len(line) >= 6 && !plainWords[strings.ToLower(line)] && !seen[line] {
				seen[line] = true
				values = append(values, line)
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
