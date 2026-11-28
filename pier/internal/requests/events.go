package requests

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Events are what a project's apps tell Pier happened: an order, a signup,
// a search. Each has a name, an optional number (an order's total) and
// properties to split by (the plan). They're kept 90 days, next to the
// requests, so a funnel can start from the people who visited.

const KeepEvents = 90 * 24 * time.Hour

const eventSchema = `
CREATE TABLE IF NOT EXISTS events (
	id      INTEGER PRIMARY KEY,
	project TEXT NOT NULL,
	env     TEXT NOT NULL DEFAULT '',
	ts      INTEGER NOT NULL,
	name    TEXT NOT NULL,
	value   REAL,
	visitor INTEGER NOT NULL DEFAULT 0,
	props   TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS events_by_name ON events(project, name, ts);
CREATE INDEX IF NOT EXISTS events_by_time ON events(project, ts);
`

// Event is one thing that happened.
type Event struct {
	Project string            `json:"-"`
	Env     string            `json:"-"`
	At      int64             `json:"at"`
	Name    string            `json:"name"`
	Value   *float64          `json:"value,omitempty"`
	Visitor int64             `json:"-"`
	Props   map[string]string `json:"props,omitempty"`
}

var eventName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,63}$`)

// Sent is an event as an app sends it: the value and properties may be any
// JSON, and user, when given, stands in for the visitor.
type Sent struct {
	Name  string         `json:"name"`
	Value any            `json:"value"`
	Props map[string]any `json:"props"`
	User  string         `json:"user"`
}

// Check turns what an app sent into an event, or says what's wrong with it.
func (s Sent) Check() (Event, error) {
	e := Event{Name: strings.TrimSpace(s.Name)}
	if !eventName.MatchString(e.Name) {
		return e, errors.New("name is up to 64 letters, digits and _ . : / -, starting with a letter or digit")
	}
	switch v := s.Value.(type) {
	case nil:
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return e, errors.New("value is a number")
		}
		e.Value = &v
	case string:
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return e, errors.New("value is a number")
		}
		e.Value = &f
	default:
		return e, errors.New("value is a number")
	}
	if len(s.Props) > 20 {
		return e, errors.New("an event has up to 20 properties")
	}
	if len(s.Props) > 0 {
		e.Props = map[string]string{}
		for k, v := range s.Props {
			if k == "" || len(k) > 40 {
				return e, errors.New("a property's name is 1 to 40 characters")
			}
			var str string
			switch x := v.(type) {
			case string:
				str = x
			case float64:
				str = strconv.FormatFloat(x, 'f', -1, 64)
			case bool:
				str = strconv.FormatBool(x)
			case nil:
				continue
			default:
				return e, fmt.Errorf("property %s is a string, a number or true or false", k)
			}
			if len(str) > 200 {
				str = str[:200]
			}
			e.Props[k] = str
		}
	}
	return e, nil
}

// AddEvent records an event with the next batch of requests. ip and agent
// make the visitor, the same one the request log counts, unless user is
// given; neither is kept.
func (l *Log) AddEvent(e Event, ip, agent, user string) {
	if e.At == 0 {
		e.At = time.Now().UnixMilli()
	}
	if l.salts != nil {
		salt := l.salts.get(time.UnixMilli(e.At))
		switch {
		case user != "":
			e.Visitor = visitor(salt, "user:"+user, "")
		case ip != "":
			e.Visitor = visitor(salt, ip, agent)
		}
	}
	l.mu.Lock()
	if len(l.pendingEvents) < 50_000 {
		l.pendingEvents = append(l.pendingEvents, e)
	}
	l.mu.Unlock()
}

func (l *Log) flushEvents(ctx context.Context, tx *sql.Tx, batch []Event) error {
	for _, e := range batch {
		props := "{}"
		if len(e.Props) > 0 {
			b, _ := json.Marshal(e.Props)
			props = string(b)
		}
		var v any
		if e.Value != nil {
			v = *e.Value
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO events(project, env, ts, name, value, visitor, props) VALUES(?, ?, ?, ?, ?, ?, ?)`,
			e.Project, e.Env, e.At, e.Name, v, e.Visitor, props); err != nil {
			return err
		}
	}
	return nil
}

// EventRow is one kind of event in a span.
type EventRow struct {
	Name     string   `json:"name"`
	Count    int64    `json:"count"`
	Visitors int64    `json:"visitors"`
	Value    *float64 `json:"value"` // the sum, when the events carry numbers
	Trend    []int64  `json:"trend"`
}

func eventWhere(project, env string, from, to int64) (string, []any) {
	w, args := "project = ? AND ts >= ? AND ts < ?", []any{project, from, to}
	if env != "" {
		w += " AND env = ?"
		args = append(args, env)
	}
	return w, args
}

// Events lists each kind of event in a span, the commonest first, with 24
// steps of counts for a trend.
func (l *Log) Events(ctx context.Context, project, env string, from, to int64) ([]EventRow, error) {
	where, args := eventWhere(project, env, from, to)
	rows, err := l.db.QueryContext(ctx, `SELECT name, count(*), count(DISTINCT CASE WHEN visitor <> 0 THEN visitor END), sum(value)
		FROM events WHERE `+where+` GROUP BY name ORDER BY 2 DESC, 1 LIMIT 100`, args...)
	if err != nil {
		return nil, err
	}
	out := []EventRow{}
	index := map[string]int{}
	for rows.Next() {
		var r EventRow
		var v sql.NullFloat64
		if err := rows.Scan(&r.Name, &r.Count, &r.Visitors, &v); err != nil {
			rows.Close()
			return nil, err
		}
		if v.Valid {
			x := v.Float64
			r.Value = &x
		}
		r.Trend = make([]int64, 24)
		index[r.Name] = len(out)
		out = append(out, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	step := max(1, (to-from)/24)
	rows, err = l.db.QueryContext(ctx, `SELECT name, (ts - ?) / ?, count(*) FROM events WHERE `+where+` GROUP BY 1, 2`, append([]any{from, step}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var i int
		var n int64
		if err := rows.Scan(&name, &i, &n); err != nil {
			return nil, err
		}
		if k, ok := index[name]; ok && i >= 0 && i < 24 {
			out[k].Trend[i] = n
		}
	}
	return out, rows.Err()
}

// EventDetail is one kind of event over a span: its count and value per
// step, split by one property's commonest values, and that property's
// breakdown.
type EventDetail struct {
	Name      string        `json:"name"`
	By        string        `json:"by"`
	Props     []string      `json:"props"` // the properties these events carry
	Step      int64         `json:"step"`
	Steps     []int64       `json:"steps"`
	Series    []EventSeries `json:"series"`
	Breakdown []EventSplit  `json:"breakdown"`
	Count     int64         `json:"count"`
	Value     *float64      `json:"value"`
}

type EventSeries struct {
	Key    string    `json:"key"`
	Counts []int64   `json:"counts"`
	Values []float64 `json:"values"`
}

type EventSplit struct {
	Key   string   `json:"key"`
	Count int64    `json:"count"`
	Value *float64 `json:"value"`
}

const otherKey = "Other"

func propPath(key string) string { return `$."` + strings.ReplaceAll(key, `"`, ``) + `"` }

// Event reads one kind of event, split by the property by ("" for none).
func (l *Log) Event(ctx context.Context, project, env, name, by string, from, to int64) (EventDetail, error) {
	d := EventDetail{Name: name, By: by, Props: []string{}, Series: []EventSeries{}, Breakdown: []EventSplit{}}
	where, args := eventWhere(project, env, from, to)
	where += " AND name = ?"
	args = append(args, name)
	// The properties seen on the latest thousand of these events.
	rows, err := l.db.QueryContext(ctx, `SELECT DISTINCT j.key FROM (SELECT props FROM events WHERE `+where+` ORDER BY ts DESC LIMIT 1000) e, json_each(e.props) j ORDER BY 1`, args...)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			rows.Close()
			return d, err
		}
		d.Props = append(d.Props, k)
	}
	rows.Close()
	key := `''`
	keyArgs := []any{}
	if by != "" {
		key = `coalesce(json_extract(props, ?), '')`
		keyArgs = []any{propPath(by)}
	}
	rows, err = l.db.QueryContext(ctx, `SELECT `+key+`, count(*), sum(value) FROM events WHERE `+where+` GROUP BY 1 ORDER BY 2 DESC, 1`, append(keyArgs, args...)...)
	if err != nil {
		return d, err
	}
	top := map[string]bool{}
	var other EventSplit
	var total float64
	var anyValue bool
	for rows.Next() {
		var s EventSplit
		var v sql.NullFloat64
		if err := rows.Scan(&s.Key, &s.Count, &v); err != nil {
			rows.Close()
			return d, err
		}
		d.Count += s.Count
		if v.Valid {
			x := v.Float64
			s.Value = &x
			total += x
			anyValue = true
		}
		if len(d.Breakdown) < 4 || by == "" {
			top[s.Key] = true
			d.Breakdown = append(d.Breakdown, s)
		} else {
			other.Key, other.Count = otherKey, other.Count+s.Count
			if s.Value != nil {
				x := *s.Value
				if other.Value != nil {
					x += *other.Value
				}
				other.Value = &x
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return d, err
	}
	if other.Count > 0 {
		d.Breakdown = append(d.Breakdown, other)
	}
	if anyValue {
		d.Value = &total
	}
	n := 24
	d.Step = max(1, (to-from)/int64(n))
	for i := 0; i < n; i++ {
		d.Steps = append(d.Steps, from+int64(i)*d.Step)
	}
	series := map[string]*EventSeries{}
	for _, b := range d.Breakdown {
		series[b.Key] = &EventSeries{Key: b.Key, Counts: make([]int64, n), Values: make([]float64, n)}
	}
	rows, err = l.db.QueryContext(ctx, `SELECT `+key+`, (ts - ?) / ?, count(*), coalesce(sum(value), 0) FROM events WHERE `+where+` GROUP BY 1, 2`,
		append(append(keyArgs, from, d.Step), args...)...)
	if err != nil {
		return d, err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var i int
		var c int64
		var v float64
		if err := rows.Scan(&k, &i, &c, &v); err != nil {
			return d, err
		}
		if !top[k] {
			k = otherKey
		}
		if s := series[k]; s != nil && i >= 0 && i < n {
			s.Counts[i] += c
			s.Values[i] += v
		}
	}
	for _, b := range d.Breakdown {
		d.Series = append(d.Series, *series[b.Key])
	}
	return d, rows.Err()
}

// VisitStep is the funnel step for people who opened a page, read from the
// request log rather than from an event.
const VisitStep = "$visit"

// Funnel counts the people who did each step in order: the second step
// after the first, and so on, within the span.
func (l *Log) Funnel(ctx context.Context, project, env string, steps []string, from, to int64) ([]int64, error) {
	counts := make([]int64, len(steps))
	var reached map[int64]int64 // visitor -> when they did the last step
	for i, step := range steps {
		var q string
		var args []any
		if step == VisitStep {
			where, a := base(project, env, from, to)
			q, args = `SELECT visitor, ts FROM requests WHERE `+where+` AND page = 1 AND bot = '' AND visitor <> 0 ORDER BY ts`, a
		} else {
			where, a := eventWhere(project, env, from, to)
			q, args = `SELECT visitor, ts FROM events WHERE `+where+` AND name = ? AND visitor <> 0 ORDER BY ts`, append(a, step)
		}
		rows, err := l.db.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		next := map[int64]int64{}
		for rows.Next() {
			var v, ts int64
			if err := rows.Scan(&v, &ts); err != nil {
				rows.Close()
				return nil, err
			}
			if _, done := next[v]; done {
				continue
			}
			if i == 0 {
				next[v] = ts
			} else if at, ok := reached[v]; ok && ts >= at {
				next[v] = ts
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		counts[i] = int64(len(next))
		reached = next
	}
	return counts, nil
}

// EventNames are the names of events seen in a span, for choosing funnel
// steps.
func (l *Log) EventNames(ctx context.Context, project string, from, to int64) ([]string, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT DISTINCT name FROM events WHERE project = ? AND ts >= ? AND ts < ?`, project, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	sort.Strings(out)
	return out, rows.Err()
}
