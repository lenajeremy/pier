// Package requests keeps the requests every site answers. Caddy streams its
// access log to a unix socket pierd listens on; each line names the
// deployment that served it (the X-Pier-Deployment header). Lines are kept
// 14 days, hourly counts 6 months, in a database of their own so heavy
// traffic never slows the rest of pierd. Visitors' addresses aren't kept.
package requests

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const (
	KeepLines = 14 * 24 * time.Hour
	KeepHours = 183 * 24 * time.Hour
)

const schema = `
CREATE TABLE IF NOT EXISTS requests (
	id         INTEGER PRIMARY KEY,
	project    TEXT NOT NULL,
	deployment TEXT NOT NULL,
	ts         INTEGER NOT NULL,
	method     TEXT NOT NULL,
	path       TEXT NOT NULL,
	status     INTEGER NOT NULL,
	ms         REAL NOT NULL,
	bytes      INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS requests_by_time ON requests(project, ts);
CREATE TABLE IF NOT EXISTS hours (
	project  TEXT NOT NULL,
	hour     INTEGER NOT NULL,
	requests INTEGER NOT NULL,
	e4xx     INTEGER NOT NULL,
	e5xx     INTEGER NOT NULL,
	PRIMARY KEY (project, hour)
);
`

// Line is one request.
type Line struct {
	ID         int64   `json:"id"`
	Project    string  `json:"-"`
	Deployment string  `json:"deployment"`
	At         int64   `json:"at"` // ms since the epoch
	Method     string  `json:"method"`
	Path       string  `json:"path"`
	Status     int     `json:"status"`
	MS         float64 `json:"ms"`
	Bytes      int64   `json:"bytes"`
	// What Analytics reads from the request's headers; see visitors.go.
	Host     string `json:"host,omitempty"`
	Env      string `json:"env,omitempty"` // production or preview
	Visitor  int64  `json:"-"`
	Page     bool   `json:"page,omitempty"` // a browser asked for a page
	Bot      string `json:"bot,omitempty"`
	Browser  string `json:"browser,omitempty"`
	Device   string `json:"device,omitempty"`
	Referrer string `json:"referrer,omitempty"`
	Country  string `json:"country,omitempty"`
	// Trace is the trace Caddy started for the request, when traces are
	// on; Span is Caddy's span in it, which pierd sends but doesn't keep.
	Trace string `json:"trace,omitempty"`
	Span  string `json:"-"`
	// Began is when the request came in, to the microsecond Caddy logs.
	Began time.Time `json:"-"`
}

// columns added after the first release; Open adds any an older database
// lacks.
var columns = []struct{ name, def string }{
	{"host", "TEXT NOT NULL DEFAULT ''"}, {"env", "TEXT NOT NULL DEFAULT ''"}, {"visitor", "INTEGER NOT NULL DEFAULT 0"},
	{"page", "INTEGER NOT NULL DEFAULT 0"}, {"bot", "TEXT NOT NULL DEFAULT ''"}, {"browser", "TEXT NOT NULL DEFAULT ''"},
	{"device", "TEXT NOT NULL DEFAULT ''"}, {"referrer", "TEXT NOT NULL DEFAULT ''"}, {"country", "TEXT NOT NULL DEFAULT ''"},
	{"trace", "TEXT NOT NULL DEFAULT ''"},
}

const lineCols = `id, deployment, ts, method, path, status, ms, bytes, host, env, page, bot, browser, device, referrer, country, trace`

func scanLine(row interface{ Scan(...any) error }) (Line, error) {
	var r Line
	err := row.Scan(&r.ID, &r.Deployment, &r.At, &r.Method, &r.Path, &r.Status, &r.MS, &r.Bytes, &r.Host, &r.Env, &r.Page, &r.Bot, &r.Browser, &r.Device, &r.Referrer, &r.Country, &r.Trace)
	return r, err
}

// Log holds the requests.
type Log struct {
	db      *sql.DB
	project func(deployment string) string // which project a deployment belongs to
	log     *slog.Logger
	salts   *salts
	// Target says whether a deployment was built for production or a
	// preview; nil leaves a request's environment empty.
	Target func(deployment string) string
	// Traced gets every written batch's requests that have a trace, to
	// send as spans. It runs on the writer, so it mustn't wait.
	Traced func([]Line)

	mu            sync.Mutex
	pending       []Line
	pendingEvents []Event
	changed       chan struct{}
	seen          map[string]int64 // deployment -> its latest request, ms since the epoch
}

// Open opens (and if needed creates) the requests database. project maps a
// deployment ID to its project, or "" if there's no such deployment.
func Open(path string, project func(string) string, log *slog.Logger) (*Log, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema + saltSchema + eventSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("requests schema: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("requests schema: %w", err)
	}
	return &Log{db: db, project: project, log: log, salts: &salts{db: db}, changed: make(chan struct{})}, nil
}

func migrate(db *sql.DB) error {
	have := map[string]bool{}
	rows, err := db.Query(`PRAGMA table_info(requests)`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var def any
		if err := rows.Scan(&cid, &name, &typ, &notnull, &def, &pk); err != nil {
			rows.Close()
			return err
		}
		have[name] = true
	}
	rows.Close()
	for _, c := range columns {
		if !have[c.name] {
			if _, err := db.Exec(`ALTER TABLE requests ADD COLUMN ` + c.name + ` ` + c.def); err != nil {
				return err
			}
		}
	}
	return nil
}

func (l *Log) Close() error { return l.db.Close() }

// entry is the part of Caddy's JSON access log Pier reads.
type entry struct {
	TS      float64 `json:"ts"`
	Request struct {
		Method   string              `json:"method"`
		URI      string              `json:"uri"`
		Host     string              `json:"host"`
		ClientIP string              `json:"client_ip"`
		RemoteIP string              `json:"remote_ip"`
		Headers  map[string][]string `json:"headers"`
	} `json:"request"`
	Duration    float64             `json:"duration"` // seconds
	Size        int64               `json:"size"`
	Status      int                 `json:"status"`
	RespHeaders map[string][]string `json:"resp_headers"`
	TraceID     string              `json:"traceID"`
	SpanID      string              `json:"spanID"`
}

// parse turns one access log line into a request, or false for requests
// that no deployment answered (Pier's own dashboard, unknown hosts).
func (l *Log) parse(raw []byte) (Line, bool) {
	var e entry
	if json.Unmarshal(raw, &e) != nil {
		return Line{}, false
	}
	dep := ""
	if v := e.RespHeaders["X-Pier-Deployment"]; len(v) > 0 {
		dep = v[0]
	}
	if dep == "" {
		return Line{}, false
	}
	project := l.project(dep)
	if project == "" {
		return Line{}, false
	}
	path := e.Request.URI
	if len(path) > 2048 {
		path = path[:2048]
	}
	header := func(name string) string {
		if v := e.Request.Headers[name]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	at := int64(e.TS * 1000)
	agent := header("User-Agent")
	v := classify(e.Request.Method, path, e.Request.Host, e.Status, agent, header("Referer"), header("Accept"), header("Cf-Ipcountry"))
	ip := e.Request.ClientIP
	if ip == "" {
		ip = e.Request.RemoteIP
	}
	line := Line{
		Project: project, Deployment: dep, At: at, Method: e.Request.Method,
		Path: path, Status: e.Status, MS: e.Duration * 1000, Bytes: e.Size,
		Host: hostOnly(e.Request.Host), Page: v.Page, Bot: v.Bot, Browser: v.Browser, Device: v.Device, Referrer: v.Referrer, Country: v.Country,
	}
	if isHex(e.TraceID, 32) && isHex(e.SpanID, 16) {
		line.Trace, line.Span = e.TraceID, e.SpanID
		// Caddy logs when the request finished.
		line.Began = time.UnixMicro(int64((e.TS - e.Duration) * 1e6))
	}
	if v.Bot == "" && l.salts != nil {
		line.Visitor = visitor(l.salts.get(time.UnixMilli(at)), ip, agent)
	}
	if l.Target != nil {
		line.Env = l.Target(dep)
	}
	return line, true
}

// Listen accepts Caddy's access log on a unix socket until ctx ends, and
// writes requests down once a second.
func (l *Log) Listen(ctx context.Context, socket string) error {
	os.Remove(socket)
	ln, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	os.Chmod(socket, 0o600)
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	go l.flushLoop(ctx)
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go l.read(c)
	}
}

func (l *Log) read(c net.Conn) {
	defer c.Close()
	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		if line, ok := l.parse(sc.Bytes()); ok {
			l.mu.Lock()
			l.pending = append(l.pending, line)
			l.mu.Unlock()
		}
	}
}

func (l *Log) flushLoop(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	clean := time.NewTicker(time.Hour)
	defer clean.Stop()
	l.cleanUp(ctx)
	for {
		select {
		case <-ctx.Done():
			l.Flush(context.Background())
			return
		case <-tick.C:
			if err := l.Flush(ctx); err != nil {
				l.log.Warn("write requests", "err", err)
			}
		case <-clean.C:
			l.cleanUp(ctx)
		}
	}
}

// Add records requests directly; tests and imports use it.
func (l *Log) Add(lines ...Line) {
	l.mu.Lock()
	l.pending = append(l.pending, lines...)
	if l.seen == nil {
		l.seen = map[string]int64{}
	}
	for _, line := range lines {
		if line.At > l.seen[line.Deployment] {
			l.seen[line.Deployment] = line.At
		}
	}
	l.mu.Unlock()
}

// LastSeen is when a deployment last answered a request since pierd
// started, or the zero time. It tells an idle preview from one in use.
func (l *Log) LastSeen(deployment string) time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	at, ok := l.seen[deployment]
	if !ok {
		return time.Time{}
	}
	return time.UnixMilli(at)
}

// Flush writes the requests waiting in memory.
func (l *Log) Flush(ctx context.Context) error {
	l.mu.Lock()
	batch, events := l.pending, l.pendingEvents
	l.pending, l.pendingEvents = nil, nil
	l.mu.Unlock()
	if len(batch) == 0 && len(events) == 0 {
		return nil
	}
	requeue := func() {
		const maxPending = 10_000
		l.mu.Lock()
		merged := append(batch, l.pending...)
		if len(merged) > maxPending {
			merged = merged[len(merged)-maxPending:]
		}
		l.pending = merged
		l.pendingEvents = append(events, l.pendingEvents...)
		l.mu.Unlock()
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		requeue()
		return err
	}
	defer tx.Rollback()
	for _, r := range batch {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO requests(project, deployment, ts, method, path, status, ms, bytes, host, env, visitor, page, bot, browser, device, referrer, country, trace)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.Project, r.Deployment, r.At, r.Method, r.Path, r.Status, r.MS, r.Bytes, r.Host, r.Env, r.Visitor, r.Page, r.Bot, r.Browser, r.Device, r.Referrer, r.Country, r.Trace); err != nil {
			requeue()
			return err
		}
		e4, e5 := 0, 0
		if r.Status >= 500 {
			e5 = 1
		} else if r.Status >= 400 {
			e4 = 1
		}
		hour := r.At - r.At%int64(time.Hour/time.Millisecond)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO hours(project, hour, requests, e4xx, e5xx) VALUES(?, ?, 1, ?, ?)
			ON CONFLICT(project, hour) DO UPDATE SET requests = requests + 1, e4xx = e4xx + excluded.e4xx, e5xx = e5xx + excluded.e5xx`,
			r.Project, hour, e4, e5); err != nil {
			requeue()
			return err
		}
	}
	if err := l.flushEvents(ctx, tx, events); err != nil {
		requeue()
		return err
	}
	if err := tx.Commit(); err != nil {
		requeue()
		return err
	}
	if l.Traced != nil {
		var traced []Line
		for _, r := range batch {
			if r.Trace != "" {
				traced = append(traced, r)
			}
		}
		if len(traced) > 0 {
			l.Traced(traced)
		}
	}
	l.mu.Lock()
	close(l.changed)
	l.changed = make(chan struct{})
	l.mu.Unlock()
	return nil
}

// Changed closes after the next committed batch. Subscribe before querying
// so an insert between reading the last row and waiting cannot be missed.
func (l *Log) Changed() <-chan struct{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.changed
}

func (l *Log) cleanUp(ctx context.Context) {
	now := time.Now()
	if _, err := l.db.ExecContext(ctx, `DELETE FROM requests WHERE ts < ?`, now.Add(-KeepLines).UnixMilli()); err != nil {
		l.log.Warn("clear old requests", "err", err)
	}
	if _, err := l.db.ExecContext(ctx, `DELETE FROM hours WHERE hour < ?`, now.Add(-KeepHours).UnixMilli()); err != nil {
		l.log.Warn("clear old request counts", "err", err)
	}
	if _, err := l.db.ExecContext(ctx, `DELETE FROM events WHERE ts < ?`, now.Add(-KeepEvents).UnixMilli()); err != nil {
		l.log.Warn("clear old events", "err", err)
	}
}

// Filter narrows the list of requests.
type Filter struct {
	From, To int64  // ms; To 0 means now
	Status   string // "", 2xx, 3xx, 4xx, 5xx or errors
	Query    string // part of the path
	After    int64  // only requests newer than this ID, for following
	Limit    int
	Forward  bool    // read the earliest page after a follow cursor
	Match    []Match // fields that must, or mustn't, hold a value
}

// Match is one filter on a field: status is 500, browser isn't Safari.
type Match struct {
	Field string `json:"field"`
	Value string `json:"value"`
	Not   bool   `json:"not,omitempty"`
}

// Fields are what a request can be filtered and grouped by, as SQL.
var Fields = map[string]string{
	"status": "CAST(status AS TEXT)", "method": "method", "path": pathNoQuery, "host": "host", "env": "env",
	"deployment": "deployment", "browser": "browser", "device": "device", "referrer": "referrer", "country": "country",
	"bot": "bot", "level": "CASE WHEN status >= 500 THEN 'error' WHEN status >= 400 THEN 'warning' ELSE 'info' END",
}

func (f Filter) where(project string) (string, []any) {
	w := []string{"project = ?", "ts >= ?"}
	args := []any{project, f.From}
	if f.To > 0 {
		w = append(w, "ts < ?")
		args = append(args, f.To)
	}
	switch f.Status {
	case "2xx", "3xx", "4xx", "5xx":
		lo := int(f.Status[0]-'0') * 100
		w = append(w, "status >= ? AND status < ?")
		args = append(args, lo, lo+100)
	case "errors":
		w = append(w, "status >= 400")
	default:
		if n, e := strconv.Atoi(f.Status); e == nil && n >= 100 && n <= 599 {
			w = append(w, "status = ?")
			args = append(args, n)
		}
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		w = append(w, "instr(lower(path), lower(?)) > 0")
		args = append(args, q)
	}
	if f.After > 0 {
		w = append(w, "id > ?")
		args = append(args, f.After)
	}
	for _, m := range f.Match {
		col, ok := Fields[m.Field]
		if !ok {
			continue
		}
		op := " = ?"
		if m.Not {
			op = " <> ?"
		}
		w = append(w, col+op)
		args = append(args, m.Value)
	}
	return strings.Join(w, " AND "), args
}

// Lines returns the newest requests that match, oldest first, so a list
// reads top to bottom like a terminal.
func (l *Log) Lines(ctx context.Context, project string, f Filter) ([]Line, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 200
	}
	where, args := f.where(project)
	order := " DESC"
	if f.Forward {
		order = " ASC"
	}
	rows, err := l.db.QueryContext(ctx, `SELECT `+lineCols+` FROM requests WHERE `+where+
		` ORDER BY id`+order+` LIMIT ?`, append(args, f.Limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Line
	for rows.Next() {
		r, err := scanLine(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	for i, j := 0, len(out)-1; !f.Forward && i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}

// Bucket is one point of the chart.
type Bucket struct {
	At       int64 `json:"at"` // ms, the bucket's start
	Requests int   `json:"requests"`
	Errors   int   `json:"errors"`
	E4xx     int   `json:"e4xx"`
	E5xx     int   `json:"e5xx"`
}

// MaxOffset is the furthest a viewer's clock can be from UTC.
const MaxOffset = 14 * time.Hour

// Offset reads a viewer's distance from UTC as the dashboard sends it: a
// whole number of minutes, east positive, and "" for UTC.
func Offset(minutes string) (time.Duration, error) {
	if minutes == "" {
		return 0, nil
	}
	most := int(MaxOffset / time.Minute)
	m, err := strconv.Atoi(minutes)
	if err != nil || m < -most || m > most {
		return 0, fmt.Errorf("not a whole number of minutes within %d of UTC", most)
	}
	return time.Duration(m) * time.Minute, nil
}

// Zone is where a viewer reads the chart: the IANA zone the dashboard sends,
// such as Europe/London, whose midnights move with its clock changes. Older
// dashboards send only their distance from UTC (see Offset), and that
// stands in too when pierd doesn't know the zone. "Local" would be the
// server's zone, never a viewer's.
func Zone(name, minutes string) (*time.Location, error) {
	if name != "" && name != "Local" {
		if loc, err := time.LoadLocation(name); err == nil {
			return loc, nil
		}
	}
	off, err := Offset(minutes)
	if err != nil {
		return nil, err
	}
	return time.FixedZone("", int(off/time.Second)), nil
}

// midnight is when a day, given by its date in UTC, starts in loc. That's
// 00:00 nearly everywhere, but where the clocks jump forward at midnight, as
// Chile's do, 00:00 never happens and the day starts at the jump; Go answers
// with the hour before it, on the day before.
func midnight(day time.Time, loc *time.Location) time.Time {
	y, m, d := day.Date()
	t := time.Date(y, m, d, 0, 0, 0, 0, loc)
	if t.Day() != d {
		_, t = t.ZoneBounds()
	}
	return t
}

func gcd(a, b int64) int64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// Chart counts requests and errors over [from, to) in buckets: minutes for
// two hours or less (from the lines), hours up to eight days, days beyond
// (from the hourly counts, which are kept 6 months). Days are the viewer's,
// from one midnight in loc to the next, since that's how the chart names
// them, so across a clock change a day is 23 or 25 hours; the step returned
// is still a day. Minutes and hours start at the same moment everywhere that
// matters. A nil loc is UTC.
func (l *Log) Chart(ctx context.Context, project string, from, to int64, status, query string, loc *time.Location) ([]Bucket, int64, error) {
	return l.ChartOf(ctx, project, Filter{From: from, To: to, Status: status, Query: query}, loc)
}

// ChartOf is Chart for any filter, field matches included.
func (l *Log) ChartOf(ctx context.Context, project string, f Filter, loc *time.Location) ([]Bucket, int64, error) {
	from, to, status, query := f.From, f.To, f.Status, f.Query
	span := time.Duration(to-from) * time.Millisecond
	var size time.Duration
	switch {
	case span <= 2*time.Hour:
		size = time.Minute
	case span <= 8*24*time.Hour:
		size = time.Hour
	default:
		size = 24 * time.Hour
	}
	step, hour := size.Milliseconds(), time.Hour.Milliseconds()
	// starts holds where each bucket starts, and then where the one after
	// the last would. Lines are counted in pieces that no start falls
	// inside: minutes or hours, and for days the most that divides every
	// midnight's place in its UTC hour, which is half an hour in India.
	var starts []int64
	piece := step
	if size < 24*time.Hour {
		for at := from - from%step; ; at += step {
			starts = append(starts, at)
			if at >= to {
				break
			}
		}
	} else {
		if loc == nil {
			loc = time.UTC
		}
		y, m, d := time.UnixMilli(from).In(loc).Date()
		piece = hour
		for day := time.Date(y, m, d, 0, 0, 0, 0, time.UTC); ; day = day.AddDate(0, 0, 1) {
			at := midnight(day, loc).UnixMilli()
			starts = append(starts, at)
			piece = gcd(piece, at%hour)
			if at >= to {
				break
			}
		}
	}
	buckets := make([]Bucket, len(starts)-1)
	for i := range buckets {
		buckets[i].At = starts[i]
	}
	// Whole hours can use retained counts. At a recent range boundary we
	// still have individual timestamps, so count only the part of that hour
	// inside the range (local midnight need not fall on a UTC hour).
	where, args := Filter{From: from, To: to, Status: status, Query: query, Match: f.Match}.where(project)
	linesSQL := `SELECT ts - ts % ? AS bucket, count(*), sum(status BETWEEN 400 AND 499), sum(status >= 500)
		FROM requests WHERE ` + where
	args = append([]any{piece}, args...)
	if size != time.Minute && status == "" && strings.TrimSpace(query) == "" && len(f.Match) == 0 {
		first, last := from-from%hour, to
		cutoff := time.Now().Add(-KeepLines).UnixMilli()
		if first < from && first >= cutoff {
			first += hour
		}
		if to-to%hour >= cutoff {
			last = to - to%hour
		}
		// Where midnight is a half or three quarters of an hour off UTC's
		// hours, as in India, it falls inside one of the stored hours. While
		// the lines are kept, that hour is counted from them, each request
		// on its own side of midnight. Older than that, only the hour's total
		// is left, and all of it goes to the day that holds the hour's
		// middle: up to half an hour of requests can be counted a day out.
		// A clock change can move midnight into an hour or out of one, so
		// each midnight is looked at on its own. SQLite takes an empty list
		// after IN, for zones whose midnights are all on the hour.
		recent := cutoff - cutoff%hour + hour // the first hour whose lines are all kept
		var split []any
		for _, at := range starts {
			if h := at - at%hour; h != at && h >= recent {
				split = append(split, h)
			}
		}
		list := strings.TrimPrefix(strings.Repeat(", ?", len(split)), ", ")
		linesSQL += ` AND (ts < ? OR ts >= ? OR ts - ts % ? IN (` + list + `)) GROUP BY bucket
			UNION ALL SELECT hour + ? AS bucket, sum(requests), sum(e4xx), sum(e5xx)
			FROM hours WHERE project = ? AND hour >= ? AND hour < ? AND hour NOT IN (` + list + `)`
		args = append(append(args, first, last, hour), split...)
		args = append(append(args, hour/2, project, first, last), split...)
	}
	rows, err := l.db.QueryContext(ctx, linesSQL+" GROUP BY bucket", args...)
	if err != nil {
		return nil, step, err
	}
	defer rows.Close()
	for rows.Next() {
		var at int64
		var count, e4, e5 int
		if err := rows.Scan(&at, &count, &e4, &e5); err != nil {
			return nil, step, err
		}
		// at is where a piece of lines starts, or a stored hour's middle: it
		// counts for the last bucket that starts at or before it.
		idx, found := slices.BinarySearch(starts, at)
		if !found {
			idx--
		}
		if idx < 0 || idx >= len(buckets) {
			continue
		}
		b := &buckets[idx]
		b.Requests += count
		b.Errors += e4 + e5
		b.E4xx += e4
		b.E5xx += e5
	}
	return buckets, step, rows.Err()
}

// Totals is the requests and errors since a time, from the hourly counts.
type Totals struct {
	Requests, E4xx, E5xx int
}

// Since counts a project's requests in [from, to), by hour.
func (l *Log) Since(ctx context.Context, project string, from, to int64) (Totals, error) {
	var t Totals
	err := l.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(requests),0), COALESCE(SUM(e4xx),0), COALESCE(SUM(e5xx),0) FROM hours WHERE project = ? AND hour >= ? AND hour < ?`,
		project, from, to).Scan(&t.Requests, &t.E4xx, &t.E5xx)
	if errors.Is(err, sql.ErrNoRows) {
		return t, nil
	}
	return t, err
}

// Values is how often each value of a field turns up among the requests a
// filter leaves, most first; the filter's own matches on that field are
// left out, so every value stays in reach.
func (l *Log) Values(ctx context.Context, project, field string, f Filter, top int) ([]Count, int64, error) {
	col, ok := Fields[field]
	if !ok {
		return nil, 0, fmt.Errorf("there's no field %q", field)
	}
	var keep []Match
	for _, m := range f.Match {
		if m.Field != field {
			keep = append(keep, m)
		}
	}
	f.Match, f.After = keep, 0
	where, args := f.where(project)
	rows, err := l.db.QueryContext(ctx, `SELECT `+col+`, count(*) FROM requests WHERE `+where+` GROUP BY 1 ORDER BY 2 DESC, 1`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Count{}
	var rest int64
	for rows.Next() {
		var c Count
		if err := rows.Scan(&c.Name, &c.Count); err != nil {
			return nil, 0, err
		}
		if len(out) < top {
			out = append(out, c)
		} else {
			rest += c.Count
		}
	}
	return out, rest, rows.Err()
}

// isHex reports whether s is n lowercase hex digits and not all zeros, as
// a trace or span ID is.
func isHex(s string, n int) bool {
	if len(s) != n || strings.Trim(s, "0") == "" {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// ByTrace finds the requests that started these traces, between from and
// to (ms since the epoch).
func (l *Log) ByTrace(ctx context.Context, project string, traces []string, from, to int64) (map[string]Line, error) {
	out := map[string]Line{}
	for len(traces) > 0 {
		n := min(len(traces), 400)
		chunk := traces[:n]
		traces = traces[n:]
		args := []any{project, from, to}
		marks := make([]string, len(chunk))
		for i, t := range chunk {
			marks[i] = "?"
			args = append(args, t)
		}
		rows, err := l.db.QueryContext(ctx, `SELECT `+lineCols+` FROM requests WHERE project = ? AND ts BETWEEN ? AND ? AND trace IN (`+strings.Join(marks, ",")+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			r, err := scanLine(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			out[r.Trace] = r
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}
