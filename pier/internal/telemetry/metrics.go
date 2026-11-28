package telemetry

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"pier/internal/promq"
)

// Metric is one metric family a project's apps send.
type Metric struct {
	Name string `json:"name"`           // the family: a histogram's series are Name_bucket, _sum and _count
	Type string `json:"type"`           // counter, gauge, histogram, summary, or unknown
	Unit string `json:"unit,omitempty"` // seconds, milliseconds, bytes, ratio...
}

func (m *Manager) prom(ctx context.Context, path string, q url.Values, out any) error {
	body, err := m.get(ctx, Prometheus, promAPI, path, q, "")
	if err != nil {
		var qe QueryError
		if asQuery(err, &qe) {
			// Prometheus explains a bad query in JSON.
			var res struct {
				Error string `json:"error"`
			}
			if json.Unmarshal([]byte(qe.Msg), &res) == nil && res.Error != "" {
				return QueryError{res.Error}
			}
		}
		return err
	}
	var res struct {
		Status string          `json:"status"`
		Error  string          `json:"error"`
		Data   json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return err
	}
	if res.Status != "success" {
		return QueryError{res.Error}
	}
	return json.Unmarshal(res.Data, out)
}

func asQuery(err error, qe *QueryError) bool {
	q, ok := err.(QueryError)
	if ok {
		*qe = q
	}
	return ok
}

// Metrics lists the metric families a project's apps sent in the last day.
// Prometheus keeps no metadata for what arrives over OTLP, so a family's
// type and unit come from its series' names, as OTLP names them.
func (m *Manager) Metrics(ctx context.Context, project string) ([]Metric, error) {
	now := time.Now()
	var names []string
	q := url.Values{"match[]": {promq.Selector(project, "")}, "start": {strconv.FormatInt(now.Add(-24*time.Hour).Unix(), 10)}, "end": {strconv.FormatInt(now.Unix(), 10)}}
	if err := m.prom(ctx, "/api/v1/label/__name__/values", q, &names); err != nil {
		return nil, err
	}
	return families(names), nil
}

func families(names []string) []Metric {
	have := map[string]bool{}
	for _, n := range names {
		have[n] = true
	}
	out := []Metric{}
	seen := map[string]bool{}
	for _, n := range names {
		if n == "target_info" {
			continue
		}
		f := Metric{Name: n, Type: "gauge"}
		switch {
		case strings.HasSuffix(n, "_bucket") && have[strings.TrimSuffix(n, "_bucket")+"_count"]:
			f = Metric{Name: strings.TrimSuffix(n, "_bucket"), Type: "histogram"}
		case strings.HasSuffix(n, "_count") || strings.HasSuffix(n, "_sum"):
			base := strings.TrimSuffix(strings.TrimSuffix(n, "_count"), "_sum")
			if have[base+"_bucket"] {
				continue
			}
			if have[base+"_count"] && have[base+"_sum"] {
				f = Metric{Name: base, Type: "summary"}
			} else if strings.HasSuffix(n, "_count") {
				f.Type = "counter"
			}
		case strings.HasSuffix(n, "_total"):
			f.Type = "counter"
		}
		if seen[f.Name] {
			continue
		}
		seen[f.Name] = true
		f.Unit = unitOf(strings.TrimSuffix(f.Name, "_total"))
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// unitOf reads the unit OTLP puts at the end of a metric's name.
func unitOf(name string) string {
	for _, u := range []string{"seconds", "milliseconds", "microseconds", "nanoseconds", "bytes", "ratio", "percent"} {
		if strings.HasSuffix(name, "_"+u) {
			return u
		}
	}
	return ""
}

// Labels lists the labels a metric's series have in the last hour, and
// their values, leaving out the ones Pier sets for every series.
func (m *Manager) Labels(ctx context.Context, project, metric string) (map[string][]string, error) {
	now := time.Now()
	var series []map[string]string
	q := url.Values{"match[]": {promq.Selector(project, metric)}, "start": {strconv.FormatInt(now.Add(-time.Hour).Unix(), 10)}, "end": {strconv.FormatInt(now.Unix(), 10)}, "limit": {"2000"}}
	if err := m.prom(ctx, "/api/v1/series", q, &series); err != nil {
		return nil, err
	}
	seen := map[string]map[string]bool{}
	for _, s := range series {
		for k, v := range s {
			if k == "__name__" || k == promq.ProjectLabel {
				continue
			}
			if seen[k] == nil {
				seen[k] = map[string]bool{}
			}
			seen[k][v] = true
		}
	}
	out := map[string][]string{}
	for k, vs := range seen {
		for v := range vs {
			out[k] = append(out[k], v)
		}
		sort.Strings(out[k])
	}
	return out, nil
}

// Series is one line of a query's result.
type Series struct {
	Labels map[string]string `json:"labels"`
	Values [][2]*float64     `json:"values"` // [ms since the epoch, value or null]
}

// Result is a range query's answer.
type Result struct {
	Query  string   `json:"query"` // as run, limited to the project
	Step   int64    `json:"step"`  // ms between points
	Series []Series `json:"series"`
}

// Query runs PromQL over a project's metrics, limited to its series.
func (m *Manager) Query(ctx context.Context, project, env, query string, from, to time.Time) (Result, error) {
	scoped, err := promq.Scope(query, project, env)
	if err != nil {
		return Result{}, QueryError{err.Error()}
	}
	span := to.Sub(from)
	step := max(15*time.Second, (span / 240).Round(time.Second))
	q := url.Values{"query": {scoped}, "start": {strconv.FormatInt(from.Unix(), 10)}, "end": {strconv.FormatInt(to.Unix(), 10)}, "step": {strconv.FormatInt(int64(step.Seconds()), 10)}}
	var data struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Values [][2]any          `json:"values"`
		} `json:"result"`
	}
	if err := m.prom(ctx, "/api/v1/query_range", q, &data); err != nil {
		return Result{}, err
	}
	res := Result{Query: scoped, Step: step.Milliseconds(), Series: []Series{}}
	for _, r := range data.Result {
		labels := map[string]string{}
		for k, v := range r.Metric {
			if k != promq.ProjectLabel {
				labels[k] = v
			}
		}
		s := Series{Labels: labels}
		for _, p := range r.Values {
			at, _ := p[0].(float64)
			ms := math.Round(at * 1000)
			var v *float64
			if str, ok := p[1].(string); ok {
				if f, err := strconv.ParseFloat(str, 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
					v = &f
				}
			}
			s.Values = append(s.Values, [2]*float64{&ms, v})
		}
		res.Series = append(res.Series, s)
	}
	return res, nil
}

// instant runs PromQL at one moment, limited to the project.
func (m *Manager) instant(ctx context.Context, project, env, query string, at time.Time) ([]struct {
	Metric map[string]string `json:"metric"`
	Value  [2]any            `json:"value"`
}, error) {
	scoped, err := promq.Scope(query, project, env)
	if err != nil {
		return nil, QueryError{err.Error()}
	}
	var data struct {
		Result []struct {
			Metric map[string]string `json:"metric"`
			Value  [2]any            `json:"value"`
		} `json:"result"`
	}
	if err := m.prom(ctx, "/api/v1/query", url.Values{"query": {scoped}, "time": {strconv.FormatInt(at.Unix(), 10)}}, &data); err != nil {
		return nil, err
	}
	return data.Result, nil
}

// MapNode is a service, database or server outside Pier on the map.
type MapNode struct {
	Name   string  `json:"name"`
	Kind   string  `json:"kind"`   // router, service, database or external
	Rate   float64 `json:"rate"`   // calls a second
	Errors float64 `json:"errors"` // the share that failed
	P95    float64 `json:"p95_ms"`
}

// MapEdge is calls from one to another.
type MapEdge struct {
	From   string  `json:"from"`
	To     string  `json:"to"`
	Kind   string  `json:"kind"` // "", database or external
	Rate   float64 `json:"rate"`
	Errors float64 `json:"errors"`
	P95    float64 `json:"p95_ms"`
}

// ServiceMap reads who called whom between from and to, and how it went.
func (m *Manager) ServiceMap(ctx context.Context, project, env string, from, to time.Time) ([]MapNode, []MapEdge, error) {
	secs := max(int(to.Sub(from).Seconds()), 60)
	window := fmt.Sprintf("[%ds]", secs)
	num := func(v [2]any) float64 {
		s, _ := v[1].(string)
		f, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 0
		}
		return f
	}
	read := func(q string) (map[string]float64, map[string]map[string]string, error) {
		res, err := m.instant(ctx, project, env, q, to)
		if err != nil {
			return nil, nil, err
		}
		vals, labels := map[string]float64{}, map[string]map[string]string{}
		for _, r := range res {
			k := r.Metric["client"] + "\x00" + r.Metric["server"] + "\x00" + r.Metric["connection"] + "\x00" + r.Metric["service"]
			vals[k] += num(r.Value)
			labels[k] = r.Metric
		}
		return vals, labels, nil
	}
	by := map[string]string{"pier_graph": "client, server, connection", "pier_service": "service"}
	type set struct{ calls, errs, p95 map[string]float64 }
	sets := map[string]set{}
	var labels map[string]map[string]string
	for prefix, group := range by {
		calls, l, err := read(fmt.Sprintf("sum by (%s) (increase(%s_calls_total%s))", group, prefix, window))
		if err != nil {
			return nil, nil, err
		}
		errs, _, err := read(fmt.Sprintf("sum by (%s) (increase(%s_errors_total%s))", group, prefix, window))
		if err != nil {
			return nil, nil, err
		}
		p95, _, err := read(fmt.Sprintf("histogram_quantile(0.95, sum by (le, %s) (increase(%s_duration_seconds_bucket%s)))", group, prefix, window))
		if err != nil {
			return nil, nil, err
		}
		sets[prefix] = set{calls, errs, p95}
		if prefix == "pier_graph" {
			labels = l
		}
	}
	share := func(e, c float64) float64 {
		if c <= 0 {
			return 0
		}
		return min(1, e/c)
	}
	nodes := map[string]*MapNode{}
	node := func(name, kind string) *MapNode {
		n := nodes[name]
		if n == nil {
			n = &MapNode{Name: name, Kind: kind}
			nodes[name] = n
		}
		return n
	}
	svc := sets["pier_service"]
	for k, c := range svc.calls {
		name := strings.Split(k, "\x00")[3]
		kind := "service"
		if name == RouterService {
			kind = "router"
		}
		n := node(name, kind)
		n.Rate, n.Errors, n.P95 = c/float64(secs), share(svc.errs[k], c), svc.p95[k]*1000
	}
	edges := []MapEdge{}
	g := sets["pier_graph"]
	// Calls into a database or outside Pier say how that end is doing.
	into := map[string]*[3]float64{}
	for k, c := range g.calls {
		if c <= 0 {
			continue
		}
		l := labels[k]
		e := MapEdge{From: l["client"], To: l["server"], Kind: l["connection"], Rate: c / float64(secs), Errors: share(g.errs[k], c), P95: g.p95[k] * 1000}
		edges = append(edges, e)
		node(e.From, "service")
		if e.Kind != "" {
			node(e.To, e.Kind)
			t := into[e.To]
			if t == nil {
				t = &[3]float64{}
				into[e.To] = t
			}
			t[0] += c
			t[1] += g.errs[k]
			t[2] = max(t[2], e.P95)
		} else {
			node(e.To, "service")
		}
	}
	for name, t := range into {
		n := nodes[name]
		n.Rate, n.Errors, n.P95 = t[0]/float64(secs), share(t[1], t[0]), t[2]
	}
	out := make([]MapNode, 0, len(nodes))
	for _, n := range nodes {
		if n.Name == RouterService {
			n.Kind = "router"
		}
		out = append(out, *n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	sort.Slice(edges, func(i, j int) bool { return edges[i].From+edges[i].To < edges[j].From+edges[j].To })
	return out, edges, nil
}
