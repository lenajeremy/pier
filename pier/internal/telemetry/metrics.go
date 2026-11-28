package telemetry

import (
	"context"
	"encoding/json"
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
