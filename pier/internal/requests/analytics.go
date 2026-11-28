package requests

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// Overview is the Analytics tab's first page: who visited, what they read,
// how it went. Bots are counted on their own and left out of everything
// else, including response times.
type Overview struct {
	From      int64    `json:"from"`
	To        int64    `json:"to"`
	Step      int64    `json:"step"` // ms between trend points
	Requests  int64    `json:"requests"`
	Errors    int64    `json:"errors"` // 5xx
	Visitors  int64    `json:"visitors"`
	PageViews int64    `json:"page_views"`
	P95       float64  `json:"p95_ms"`
	Before    Headline `json:"before"` // the span of the same length just before
	Trend     []Point  `json:"trend"`
	Pages     List     `json:"pages"`
	Referrers List     `json:"referrers"`
	Countries List     `json:"countries"`
	Browsers  List     `json:"browsers"`
	Devices   List     `json:"devices"`
	Bots      List     `json:"bots"`
	Slowest   []Route  `json:"slowest"`
}

// Headline are the headline numbers of one span.
type Headline struct {
	Requests  int64   `json:"requests"`
	Errors    int64   `json:"errors"`
	Visitors  int64   `json:"visitors"`
	PageViews int64   `json:"page_views"`
	P95       float64 `json:"p95_ms"`
}

// Point is one step of the trend.
type Point struct {
	At        int64    `json:"at"`
	Requests  int64    `json:"requests"`
	Errors    int64    `json:"errors"`
	Visitors  int64    `json:"visitors"`
	PageViews int64    `json:"page_views"`
	P95       *float64 `json:"p95_ms"` // nil when nobody asked for anything
}

// List is the top of one breakdown and what's left after it.
type List struct {
	Top    []Count `json:"top"`
	Rest   int64   `json:"rest"`   // counted outside the top
	Others int     `json:"others"` // how many names that rest spans
}

type Count struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

// Route is one method and path with its slowest twentieth.
type Route struct {
	Route    string  `json:"route"`
	Requests int64   `json:"requests"`
	Errors   int64   `json:"errors"`
	P95      float64 `json:"p95_ms"`
}

const pathNoQuery = `CASE WHEN instr(path, '?') > 0 THEN substr(path, 1, instr(path, '?') - 1) ELSE path END`

func base(project, env string, from, to int64) (string, []any) {
	w, args := "project = ? AND ts >= ? AND ts < ?", []any{project, from, to}
	if env != "" {
		w += " AND env = ?"
		args = append(args, env)
	}
	return w, args
}

// Overview reads one project's requests from from to to (ms), all of them
// or only one environment's.
func (l *Log) Overview(ctx context.Context, project, env string, from, to int64, top int) (Overview, error) {
	if top <= 0 {
		top = 5
	}
	o := Overview{From: from, To: to}
	var err error
	cur, err := l.totals(ctx, project, env, from, to)
	if err != nil {
		return o, err
	}
	o.Requests, o.Errors, o.Visitors, o.PageViews, o.P95 = cur.Requests, cur.Errors, cur.Visitors, cur.PageViews, cur.P95
	if o.Before, err = l.totals(ctx, project, env, from-(to-from), from); err != nil {
		return o, err
	}
	if o.Trend, o.Step, err = l.trend(ctx, project, env, from, to); err != nil {
		return o, err
	}
	people := " AND bot = ''"
	pages := people + " AND page = 1"
	for _, b := range []struct {
		dst          *List
		col, where   string
		distinctUser bool
	}{
		{&o.Pages, pathNoQuery, pages, false},
		{&o.Referrers, "referrer", pages, false},
		{&o.Countries, "country", people + " AND country <> ''", true},
		{&o.Browsers, "browser", people + " AND browser <> ''", true},
		{&o.Devices, "device", people + " AND device <> ''", true},
		{&o.Bots, "bot", " AND bot <> ''", false},
	} {
		if *b.dst, err = l.breakdown(ctx, project, env, from, to, b.col, b.where, b.distinctUser, top); err != nil {
			return o, err
		}
	}
	if o.Slowest, err = l.slowest(ctx, project, env, from, to, 6); err != nil {
		return o, err
	}
	return o, nil
}

func (l *Log) totals(ctx context.Context, project, env string, from, to int64) (Headline, error) {
	var t Headline
	where, args := base(project, env, from, to)
	var people sql.NullInt64
	var errs, pv sql.NullInt64
	if err := l.db.QueryRowContext(ctx, `SELECT count(*), sum(status >= 500), sum(page = 1 AND bot = ''),
		count(DISTINCT CASE WHEN bot = '' AND visitor <> 0 THEN visitor END), sum(bot = '')
		FROM requests WHERE `+where, args...).Scan(&t.Requests, &errs, &pv, &t.Visitors, &people); err != nil {
		return t, err
	}
	t.Errors, t.PageViews = errs.Int64, pv.Int64
	if people.Int64 > 0 {
		var ms float64
		if err := l.db.QueryRowContext(ctx, `SELECT ms FROM requests WHERE `+where+` AND bot = '' ORDER BY ms LIMIT 1 OFFSET ?`,
			append(args, nth(people.Int64, 0.95))...).Scan(&ms); err != nil {
			return t, err
		}
		t.P95 = ms
	}
	return t, nil
}

// nth is the row a percentile falls on, counting from zero.
func nth(n int64, p float64) int64 {
	i := int64(float64(n)*p+0.999999) - 1
	if i < 0 {
		i = 0
	}
	if i >= n {
		i = n - 1
	}
	return i
}

// trend splits the span into about sixty steps of whole minutes.
func (l *Log) trend(ctx context.Context, project, env string, from, to int64) ([]Point, int64, error) {
	step := (to - from) / 60
	minute := int64(time.Minute / time.Millisecond)
	if step < minute {
		step = minute
	}
	step -= step % minute
	n := int((to - from + step - 1) / step)
	pts := make([]Point, n)
	for i := range pts {
		pts[i].At = from + int64(i)*step
	}
	where, args := base(project, env, from, to)
	rows, err := l.db.QueryContext(ctx, `SELECT (ts - ?) / ?, count(*), sum(status >= 500), sum(page = 1 AND bot = ''),
		count(DISTINCT CASE WHEN bot = '' AND visitor <> 0 THEN visitor END)
		FROM requests WHERE `+where+` GROUP BY 1`, append([]any{from, step}, args...)...)
	if err != nil {
		return nil, 0, err
	}
	for rows.Next() {
		var i int
		var p Point
		var e, pv sql.NullInt64
		if err := rows.Scan(&i, &p.Requests, &e, &pv, &p.Visitors); err != nil {
			rows.Close()
			return nil, 0, err
		}
		if i >= 0 && i < n {
			pts[i].Requests, pts[i].Errors, pts[i].PageViews, pts[i].Visitors = p.Requests, e.Int64, pv.Int64, p.Visitors
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	// The p95 of each step needs every request in it sorted, so it's only
	// worked out for a day or less.
	if to-from <= int64(24*time.Hour/time.Millisecond) {
		rows, err := l.db.QueryContext(ctx, `SELECT b, ms FROM (
			SELECT (ts - ?) / ? AS b, ms, row_number() OVER (PARTITION BY (ts - ?) / ? ORDER BY ms) AS rn, count(*) OVER (PARTITION BY (ts - ?) / ?) AS n
			FROM requests WHERE `+where+` AND bot = '') WHERE rn = max(1, CAST(n * 0.95 + 0.999999 AS INTEGER))`,
			append([]any{from, step, from, step, from, step}, args...)...)
		if err != nil {
			return nil, 0, err
		}
		for rows.Next() {
			var i int
			var ms float64
			if err := rows.Scan(&i, &ms); err != nil {
				rows.Close()
				return nil, 0, err
			}
			if i >= 0 && i < n {
				v := ms
				pts[i].P95 = &v
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, 0, err
		}
	}
	return pts, step, nil
}

func (l *Log) breakdown(ctx context.Context, project, env string, from, to int64, col, extra string, distinctUser bool, top int) (List, error) {
	where, args := base(project, env, from, to)
	measure := "count(*)"
	if distinctUser {
		measure = "count(DISTINCT CASE WHEN visitor <> 0 THEN visitor END)"
	}
	list := List{Top: []Count{}}
	rows, err := l.db.QueryContext(ctx, `SELECT `+col+`, `+measure+` FROM requests WHERE `+where+extra+` GROUP BY 1 ORDER BY 2 DESC, 1`, args...)
	if err != nil {
		return list, err
	}
	defer rows.Close()
	for rows.Next() {
		var c Count
		if err := rows.Scan(&c.Name, &c.Count); err != nil {
			return list, err
		}
		if c.Count == 0 {
			continue
		}
		if len(list.Top) < top {
			list.Top = append(list.Top, c)
		} else {
			list.Rest += c.Count
			list.Others++
		}
	}
	return list, rows.Err()
}

// slowest is the routes with the slowest p95, among the forty busiest.
func (l *Log) slowest(ctx context.Context, project, env string, from, to int64, top int) ([]Route, error) {
	where, args := base(project, env, from, to)
	rows, err := l.db.QueryContext(ctx, `WITH r AS (
			SELECT method || ' ' || `+pathNoQuery+` AS route, ms, status FROM requests WHERE `+where+` AND bot = ''),
		busy AS (SELECT route, count(*) AS n, sum(status >= 500) AS e FROM r GROUP BY route HAVING n >= 5 ORDER BY n DESC LIMIT 40),
		ranked AS (SELECT r.route, r.ms, busy.n, busy.e, row_number() OVER (PARTITION BY r.route ORDER BY r.ms) AS rn FROM r JOIN busy ON busy.route = r.route)
		SELECT route, n, e, ms FROM ranked WHERE rn = max(1, CAST(n * 0.95 + 0.999999 AS INTEGER)) ORDER BY ms DESC LIMIT ?`, append(args, top)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Route{}
	for rows.Next() {
		var r Route
		if err := rows.Scan(&r.Route, &r.Requests, &r.Errors, &r.P95); err != nil {
			return nil, err
		}
		r.Route = strings.TrimSpace(r.Route)
		out = append(out, r)
	}
	return out, rows.Err()
}
