package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"pier/internal/deploy"
	"pier/internal/store"
)

var varName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,255}$`)

// reservedVar names are set by Pier for every build, or would break host tooling.
var reservedVar = map[string]bool{
	"PATH": true, "HOME": true, "CI": true, "PWD": true,
	"NO_COLOR": true, "FORCE_COLOR": true, "YARN_CACHE_FOLDER": true, "PNPM_HOME": true,
	"NODE_OPTIONS": true, "GODEBUG": true,
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "ALL_PROXY": true, "NO_PROXY": true,
	"http_proxy": true, "https_proxy": true, "all_proxy": true, "no_proxy": true,
}

func isReservedVar(name string) bool {
	if reservedVar[name] {
		return true
	}
	for _, prefix := range []string{"npm_config_", "COREPACK_", "DOCKER_", "LD_", "DYLD_"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// MaxValue is the longest a variable's value may be.
const MaxValue = 64 << 10

type varEntry struct {
	Name      string    `json:"name"`
	Scopes    []string  `json:"scopes"`
	UpdatedAt time.Time `json:"updated_at"`
}

// variables lists a project's variables without their values: one entry
// per name, or two when production and previews hold different values. It
// also says which ones changed since production last built.
func (a *API) variables(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	ps, ok := a.findProject(w, r, name)
	if !ok {
		return
	}
	all, err := a.Store.Variables(r.Context(), name)
	if err != nil {
		a.fail(w, err)
		return
	}
	entries := []varEntry{}
	byName := map[string][]store.Variable{}
	var order []string
	for _, v := range all {
		if byName[v.Name] == nil {
			order = append(order, v.Name)
		}
		byName[v.Name] = append(byName[v.Name], v)
	}
	for _, n := range order {
		vs := byName[n]
		sort.Slice(vs, func(i, j int) bool {
			return vs[i].Scope == store.TargetProduction && vs[j].Scope != store.TargetProduction
		})
		if len(vs) == 2 && a.sameValue(name, vs[0], vs[1]) {
			e := varEntry{Name: n, Scopes: []string{store.TargetProduction, store.TargetPreview}, UpdatedAt: vs[0].UpdatedAt}
			if vs[1].UpdatedAt.After(e.UpdatedAt) {
				e.UpdatedAt = vs[1].UpdatedAt
			}
			entries = append(entries, e)
			continue
		}
		for _, v := range vs {
			entries = append(entries, varEntry{Name: n, Scopes: []string{v.Scope}, UpdatedAt: v.UpdatedAt})
		}
	}

	// What production's build doesn't have yet.
	stale := map[string]any{"production": false, "names": []string{}}
	if ps.production != nil && ps.production.Deployment != nil {
		built := ps.production.Deployment.CreatedAt
		changed, _, err := a.Store.VariablesChanged(r.Context(), name)
		if err == nil && changed.After(built) {
			var names []string
			for _, v := range all {
				if v.Scope == store.TargetProduction && v.UpdatedAt.After(built) {
					names = append(names, v.Name)
				}
			}
			stale = map[string]any{"production": true, "names": nonNil(names), "deployment": ps.production.Deployment.ID}
		}
	}
	shared, err := a.Store.SharedVariables(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	type inherited struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	}
	inheritedVars := []inherited{}
	for _, v := range shared {
		if !slices.Contains(v.Projects, name) {
			continue
		}
		var scopes []string
		for _, scope := range []string{store.TargetProduction, store.TargetPreview} {
			if (scope == store.TargetProduction && !v.Production) || (scope == store.TargetPreview && !v.Preview) {
				continue
			}
			if !slices.ContainsFunc(byName[v.Name], func(own store.Variable) bool { return own.Scope == scope }) {
				scopes = append(scopes, scope)
			}
		}
		if len(scopes) > 0 {
			inheritedVars = append(inheritedVars, inherited{v.Name, scopes})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"variables": entries, "inherited": inheritedVars, "stale": stale})
}

func (a *API) sameValue(project string, x, y store.Variable) bool {
	vx, err1 := a.Deployer.Secrets.Open(x.Value, deploy.SealedFor(project, x.Scope, x.Name))
	vy, err2 := a.Deployer.Secrets.Open(y.Value, deploy.SealedFor(project, y.Scope, y.Name))
	return err1 == nil && err2 == nil && vx == vy
}

type varRequest struct {
	Name      string   `json:"name"`
	Value     string   `json:"value"`
	Projects  []string `json:"projects"`
	KeepValue bool     `json:"keep_value"`
	Scopes    []string `json:"scopes"`
}

func readVar(w http.ResponseWriter, r *http.Request, needValue bool) (varRequest, bool) {
	var req varRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, MaxValue+4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "send the variable as JSON"})
		return req, false
	}
	req.Name = strings.TrimSpace(req.Name)
	switch {
	case !varName.MatchString(req.Name):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Use letters, numbers and underscores, starting with a letter, like API_URL.", "field": "name"})
		return req, false
	case isReservedVar(req.Name):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": req.Name + " is reserved by Pier for builds. Choose another name.", "field": "name"})
		return req, false
	case needValue && len(req.Value) > MaxValue:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Values can be up to 64 KB.", "field": "value"})
		return req, false
	}
	if len(req.Scopes) == 0 {
		req.Scopes = []string{store.TargetProduction, store.TargetPreview}
	}
	for _, s := range req.Scopes {
		if s != store.TargetProduction && s != store.TargetPreview {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Scopes are production and preview.", "field": "scopes"})
			return req, false
		}
	}
	return req, true
}

// setVariable adds or changes a variable in the scopes given.
func (a *API) setVariable(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	if _, ok := a.findProject(w, r, name); !ok {
		return
	}
	req, ok := readVar(w, r, true)
	if !ok {
		return
	}
	for _, scope := range req.Scopes {
		sealed, err := a.Deployer.Secrets.Seal(req.Value, deploy.SealedFor(name, scope, req.Name))
		if err != nil {
			a.fail(w, err)
			return
		}
		if err := a.Store.SetVariable(r.Context(), store.Variable{Project: name, Scope: scope, Name: req.Name, Value: sealed}); err != nil {
			a.fail(w, err)
			return
		}
	}
	a.event(r.Context(), name, "Changed variable", req.Name+" · "+strings.Join(req.Scopes, ", "), "")
	a.Log.Info("variable set", "project", name, "name", req.Name, "scopes", strings.Join(req.Scopes, ","))
	a.variables(w, r)
}

const maxImportBytes = 1 << 20
const maxImportVars = 256

type envPair struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// parseDotEnv parses .env / Railway Raw Editor text, supporting comments,
// optional "export " prefixes, and single- or double-quoted multiline values.
func parseDotEnv(raw string) ([]envPair, error) {
	raw = strings.TrimPrefix(raw, "\uFEFF")
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")

	var out []envPair
	index := map[string]int{}
	i, line := 0, 1
	n := len(raw)

	skipSpaces := func() {
		for i < n && (raw[i] == ' ' || raw[i] == '\t') {
			i++
		}
	}

	for i < n {
		skipSpaces()
		if i >= n {
			break
		}
		if raw[i] == '\n' {
			i++
			line++
			continue
		}
		if raw[i] == '#' {
			for i < n && raw[i] != '\n' {
				i++
			}
			continue
		}
		startLine := line
		if strings.HasPrefix(raw[i:], "export") && i+6 < n && (raw[i+6] == ' ' || raw[i+6] == '\t') {
			i += 6
			skipSpaces()
		}
		keyStart := i
		for i < n && raw[i] != '=' && raw[i] != '\n' {
			i++
		}
		if i >= n || raw[i] != '=' {
			return nil, fmt.Errorf("line %d: expected NAME=value", startLine)
		}
		key := strings.TrimSpace(raw[keyStart:i])
		i++ // consume '='
		skipSpaces()

		var val string
		if i < n && (raw[i] == '"' || raw[i] == '\'') {
			quote := raw[i]
			i++
			var b strings.Builder
			closed := false
			for i < n {
				c := raw[i]
				if c == '\n' {
					line++
				}
				if quote == '"' && c == '\\' && i+1 < n {
					next := raw[i+1]
					switch next {
					case 'n':
						b.WriteByte('\n')
					case 'r':
						b.WriteByte('\r')
					case 't':
						b.WriteByte('\t')
					case '"', '\\', '$':
						b.WriteByte(next)
					case '\n':
						line++
					default:
						b.WriteByte('\\')
						b.WriteByte(next)
					}
					i += 2
					continue
				}
				if c == quote {
					i++
					closed = true
					break
				}
				b.WriteByte(c)
				i++
			}
			if !closed {
				return nil, fmt.Errorf("line %d: unterminated quote for %s", startLine, key)
			}
			val = b.String()
			for i < n && raw[i] != '\n' {
				if raw[i] == '#' {
					for i < n && raw[i] != '\n' {
						i++
					}
					break
				}
				if raw[i] != ' ' && raw[i] != '\t' {
					return nil, fmt.Errorf("line %d: unexpected characters after closing quote for %s", line, key)
				}
				i++
			}
		} else {
			valStart := i
			for i < n && raw[i] != '\n' {
				i++
			}
			val = strings.TrimRight(raw[valStart:i], " \t")
		}
		if i < n && raw[i] == '\n' {
			i++
			line++
		}

		if !varName.MatchString(key) {
			return nil, fmt.Errorf("line %d: %q is not a valid variable name", startLine, key)
		}
		if isReservedVar(key) {
			return nil, fmt.Errorf("line %d: %s is reserved by Pier for builds", startLine, key)
		}
		if len(val) > MaxValue {
			return nil, fmt.Errorf("line %d: %s exceeds the 64 KB limit", startLine, key)
		}
		if pos, exists := index[key]; exists {
			out[pos].Value = val
		} else {
			if len(out) >= maxImportVars {
				return nil, fmt.Errorf("import at most %d variables at once", maxImportVars)
			}
			index[key] = len(out)
			out = append(out, envPair{Name: key, Value: val})
		}
	}
	return out, nil
}

// importVariables parses a pasted .env block and saves all variables to the given scopes.
func (a *API) importVariables(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	if _, ok := a.findProject(w, r, name); !ok {
		return
	}
	var req struct {
		Raw    string   `json:"raw"`
		Scopes []string `json:"scopes"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxImportBytes)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "send the .env payload as JSON"})
		return
	}
	if len(req.Scopes) == 0 {
		req.Scopes = []string{store.TargetProduction, store.TargetPreview}
	}
	for _, s := range req.Scopes {
		if s != store.TargetProduction && s != store.TargetPreview {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Scopes are production and preview.", "field": "scopes"})
			return
		}
	}
	pairs, err := parseDotEnv(req.Raw)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "field": "raw"})
		return
	}
	if len(pairs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Paste at least one NAME=value line to import.", "field": "raw"})
		return
	}
	for _, p := range pairs {
		for _, scope := range req.Scopes {
			sealed, err := a.Deployer.Secrets.Seal(p.Value, deploy.SealedFor(name, scope, p.Name))
			if err != nil {
				a.fail(w, err)
				return
			}
			if err := a.Store.SetVariable(r.Context(), store.Variable{Project: name, Scope: scope, Name: p.Name, Value: sealed}); err != nil {
				a.fail(w, err)
				return
			}
		}
	}
	summary := fmt.Sprintf("%d variables · %s", len(pairs), strings.Join(req.Scopes, ", "))
	if len(pairs) == 1 {
		summary = pairs[0].Name + " · " + strings.Join(req.Scopes, ", ")
	}
	a.event(r.Context(), name, "Imported variables", summary, "")
	a.Log.Info("variables imported", "project", name, "count", len(pairs), "scopes", strings.Join(req.Scopes, ","))
	a.variables(w, r)
}

// deleteVariable removes a variable from the scopes given.
func (a *API) deleteVariable(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	if _, ok := a.findProject(w, r, name); !ok {
		return
	}
	req, ok := readVar(w, r, false)
	if !ok {
		return
	}
	for _, scope := range req.Scopes {
		if _, err := a.Store.DeleteVariable(r.Context(), name, scope, req.Name); err != nil {
			a.fail(w, err)
			return
		}
	}
	a.event(r.Context(), name, "Removed variable", req.Name+" · "+strings.Join(req.Scopes, ", "), "")
	a.Log.Info("variable deleted", "project", name, "name", req.Name)
	a.variables(w, r)
}

// revealVariable returns one value, when the owner asks to see it.
func (a *API) revealVariable(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	if _, ok := a.findProject(w, r, name); !ok {
		return
	}
	req, ok := readVar(w, r, false)
	if !ok || len(req.Scopes) == 0 {
		return
	}
	all, err := a.Store.Variables(r.Context(), name)
	if err != nil {
		a.fail(w, err)
		return
	}
	i := slices.IndexFunc(all, func(v store.Variable) bool { return v.Name == req.Name && v.Scope == req.Scopes[0] })
	if i < 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "There's no " + req.Name + " in " + req.Scopes[0] + "."})
		return
	}
	value, err := a.Deployer.Secrets.Open(all[i].Value, deploy.SealedFor(name, req.Scopes[0], req.Name))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"value": value})
}

// redeploy rebuilds a deployment's source with today's settings and
// variables: production's, when no deployment is named. It answers as soon
// as the new deployment exists; its page follows the build.
func (a *API) redeploy(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	ps, ok := a.findProject(w, r, name)
	if !ok {
		return
	}
	id := readID(r)
	target := ""
	if id == "" {
		if ps.production == nil || ps.production.Deployment == nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "Production isn't serving anything yet, so there's nothing to redeploy."})
			return
		}
		id, target = ps.production.Deployment.ID, store.TargetProduction
	}
	started := make(chan string, 1)
	failed := make(chan error, 1)
	go func() {
		err := a.Deployer.Redeploy(context.WithoutCancel(r.Context()), name, id, target, userFrom(r.Context()).DisplayName(), func(newID string) { started <- newID }, io.Discard)
		if err != nil {
			a.Log.Info("redeploy failed", "project", name, "from", id, "err", err)
			failed <- err
		}
	}()
	select {
	case newID := <-started:
		writeJSON(w, http.StatusAccepted, map[string]string{"id": newID, "from": id})
	case err := <-failed:
		if errors.Is(err, deploy.ErrNoSource) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": id + "'s source isn't kept any more, so it can't be rebuilt. Deploy again from your computer."})
			return
		}
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Couldn't start the build: " + err.Error()})
	case <-time.After(30 * time.Second):
		writeJSON(w, http.StatusAccepted, map[string]string{"from": id, "note": "the build is waiting for another one on this project to finish"})
	}
}
