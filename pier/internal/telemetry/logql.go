package telemetry

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"time"
)

// Entry is one log line with everything Loki knows about it: its labels,
// structured metadata and the fields parsed from it.
type Entry struct {
	At     float64           `json:"at"` // ms since the epoch
	NS     string            `json:"ns"` // the exact time, to page from
	Labels map[string]string `json:"labels"`
	Line   string            `json:"line"`
}

func (m *Manager) loki(ctx context.Context, project, path string, q url.Values, out any) error {
	body, err := m.get(tenant(ctx, project), Loki, lokiAPI, path, q, "")
	if err != nil {
		if err == ErrNoTrace {
			return QueryError{"Loki didn't find that"}
		}
		return err
	}
	return json.Unmarshal(body, out)
}

func nanos(t time.Time) string { return strconv.FormatInt(t.UnixNano(), 10) }

// Lines runs a LogQL log query, newest first.
func (m *Manager) Lines(ctx context.Context, project, query string, from, to time.Time, limit int) ([]Entry, error) {
	var res struct {
		Data struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Stream map[string]string `json:"stream"`
				Values [][]string        `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	q := url.Values{"query": {query}, "start": {nanos(from)}, "end": {nanos(to)}, "limit": {strconv.Itoa(limit)}, "direction": {"backward"}}
	if err := m.loki(ctx, project, "/loki/api/v1/query_range", q, &res); err != nil {
		return nil, err
	}
	if res.Data.ResultType != "" && res.Data.ResultType != "streams" {
		return nil, QueryError{"that's a metric query; Logs takes a log query, like {service_name=\"api\"} |= \"error\""}
	}
	out := []Entry{}
	for _, r := range res.Data.Result {
		labels := map[string]string{}
		for k, v := range r.Stream {
			if k != "pier_project" && k != "observed_timestamp" && k != "__error__" && k != "__error_details__" {
				labels[k] = v
			}
		}
		for _, v := range r.Values {
			if len(v) < 2 {
				continue
			}
			ns, _ := strconv.ParseInt(v[0], 10, 64)
			out = append(out, Entry{At: float64(ns) / 1e6, NS: v[0], Labels: labels, Line: v[1]})
		}
	}
	ns := func(e Entry) int64 { n, _ := strconv.ParseInt(e.NS, 10, 64); return n }
	sort.SliceStable(out, func(i, j int) bool { return ns(out[i]) > ns(out[j]) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Bucket is how many lines of each level fell in one step.
type Bucket struct {
	At     float64          `json:"at"`
	Levels map[string]int64 `json:"levels"`
}

// Volume counts a log query's lines by level over time.
func (m *Manager) Volume(ctx context.Context, project, query string, from, to time.Time) ([]Bucket, int64, error) {
	step := max(time.Second, (to.Sub(from) / 60).Round(time.Second))
	metric := fmt.Sprintf("sum by (detected_level) (count_over_time(%s [%ds]))", query, int(step.Seconds()))
	var res struct {
		Data struct {
			Result []struct {
				Metric map[string]string `json:"metric"`
				Values [][2]any          `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	q := url.Values{"query": {metric}, "start": {nanos(from)}, "end": {nanos(to)}, "step": {strconv.Itoa(int(step.Seconds()))}}
	if err := m.loki(ctx, project, "/loki/api/v1/query_range", q, &res); err != nil {
		return nil, 0, err
	}
	at := map[float64]map[string]int64{}
	for _, r := range res.Data.Result {
		level := r.Metric["detected_level"]
		if level == "" {
			level = "unknown"
		}
		for _, v := range r.Values {
			t, _ := v[0].(float64)
			s, _ := v[1].(string)
			n, _ := strconv.ParseInt(s, 10, 64)
			if at[t] == nil {
				at[t] = map[string]int64{}
			}
			at[t][level] += n
		}
	}
	// Every step, empty ones too, so the chart keeps its time axis.
	out := []Bucket{}
	for t := from.Truncate(step); !t.After(to); t = t.Add(step) {
		sec := float64(t.Unix())
		levels := at[sec]
		if levels == nil {
			levels = map[string]int64{}
		}
		out = append(out, Bucket{At: sec * 1000, Levels: levels})
	}
	return out, step.Milliseconds(), nil
}

// Field is something Loki found in a query's lines.
type Field struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Cardinality int      `json:"cardinality"`
	Parsers     []string `json:"parsers,omitempty"` // json or logfmt when it's parsed from the line
	Label       bool     `json:"label,omitempty"`   // an indexed label, which a stream selector can name
}

// Fields lists the fields in a log query's lines.
func (m *Manager) Fields(ctx context.Context, project, query string, from, to time.Time) ([]Field, error) {
	var res struct {
		Fields []struct {
			Label       string   `json:"label"`
			Type        string   `json:"type"`
			Cardinality int      `json:"cardinality"`
			Parsers     []string `json:"parsers"`
		} `json:"fields"`
	}
	q := url.Values{"query": {query}, "start": {nanos(from)}, "end": {nanos(to)}, "limit": {"1000"}}
	if err := m.loki(ctx, project, "/loki/api/v1/detected_fields", q, &res); err != nil {
		return nil, err
	}
	// Labels aren't detected fields; ask for those too.
	var labels struct {
		Data []string `json:"data"`
	}
	if err := m.loki(ctx, project, "/loki/api/v1/labels", url.Values{"start": {nanos(from)}, "end": {nanos(to)}}, &labels); err != nil {
		return nil, err
	}
	out := []Field{}
	seen := map[string]bool{}
	for _, l := range labels.Data {
		if !seen[l] {
			seen[l] = true
			out = append(out, Field{Name: l, Type: "string", Label: true})
		}
	}
	for _, f := range res.Fields {
		if seen[f.Label] || f.Label == "pier_project" || f.Label == "observed_timestamp" {
			continue
		}
		seen[f.Label] = true
		out = append(out, Field{Name: f.Label, Type: f.Type, Cardinality: f.Cardinality, Parsers: f.Parsers})
	}
	return out, nil
}

var fieldName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// Value is how often a field had one value.
type Value struct {
	Value string `json:"value"`
	Count int64  `json:"count"`
}

// Values counts a field's commonest values in a log query's lines. parsed
// is the parser the field comes from, json or logfmt, if it's in the line.
func (m *Manager) Values(ctx context.Context, project, query, field, parsed string, from, to time.Time) ([]Value, error) {
	if !fieldName.MatchString(field) {
		return nil, QueryError{"that isn't a field's name"}
	}
	pipe := query
	switch parsed {
	case "json":
		pipe += ` | json | __error__=""`
	case "logfmt":
		pipe += ` | logfmt | __error__=""`
	}
	span := max(int(to.Sub(from).Seconds()), 1)
	metric := fmt.Sprintf(`topk(10, sum by (%s) (count_over_time(%s | %s!="" [%ds])))`, field, pipe, field, span)
	var res struct {
		Data struct {
			Result []struct {
				Metric map[string]string `json:"metric"`
				Value  [2]any            `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := m.loki(ctx, project, "/loki/api/v1/query", url.Values{"query": {metric}, "time": {nanos(to)}}, &res); err != nil {
		return nil, err
	}
	out := []Value{}
	for _, r := range res.Data.Result {
		s, _ := r.Value[1].(string)
		n, _ := strconv.ParseInt(s, 10, 64)
		out = append(out, Value{Value: r.Metric[field], Count: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return out, nil
}

// LogMetric runs a LogQL metric query, like count_over_time, and answers
// as a PromQL range query does.
func (m *Manager) LogMetric(ctx context.Context, project, query string, from, to time.Time, step time.Duration) (Result, error) {
	step = stepFor(from, to, step)
	var res struct {
		Data struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string `json:"metric"`
				Values [][2]any          `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	q := url.Values{"query": {query}, "start": {nanos(from)}, "end": {nanos(to)}, "step": {strconv.Itoa(int(step.Seconds()))}}
	if err := m.loki(ctx, project, "/loki/api/v1/query_range", q, &res); err != nil {
		return Result{}, err
	}
	if res.Data.ResultType == "streams" {
		return Result{}, QueryError{"that's a log query; a chart needs a metric query, like sum(count_over_time({service_name=\"api\"} |= \"error\" [1m]))"}
	}
	out := Result{Query: query, Step: step.Milliseconds(), Series: []Series{}}
	for _, r := range res.Data.Result {
		s := Series{Labels: r.Metric}
		for _, p := range r.Values {
			at, _ := p[0].(float64)
			ms := at * 1000
			var v *float64
			if str, ok := p[1].(string); ok {
				if f, err := strconv.ParseFloat(str, 64); err == nil {
					v = &f
				}
			}
			s.Values = append(s.Values, [2]*float64{&ms, v})
		}
		out.Series = append(out.Series, s)
	}
	return out, nil
}
