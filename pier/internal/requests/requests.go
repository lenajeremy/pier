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
}

// Log holds the requests.
type Log struct {
	db      *sql.DB
	project func(deployment string) string // which project a deployment belongs to
	log     *slog.Logger

	mu      sync.Mutex
	pending []Line
}

// Open opens (and if needed creates) the requests database. project maps a
// deployment ID to its project, or "" if there's no such deployment.
func Open(path string, project func(string) string, log *slog.Logger) (*Log, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("requests schema: %w", err)
	}
	return &Log{db: db, project: project, log: log}, nil
}

func (l *Log) Close() error { return l.db.Close() }

// entry is the part of Caddy's JSON access log Pier reads.
type entry struct {
	TS      float64 `json:"ts"`
	Request struct {
		Method string `json:"method"`
		URI    string `json:"uri"`
	} `json:"request"`
	Duration    float64             `json:"duration"` // seconds
	Size        int64               `json:"size"`
	Status      int                 `json:"status"`
	RespHeaders map[string][]string `json:"resp_headers"`
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
	return Line{
		Project: project, Deployment: dep, At: int64(e.TS * 1000), Method: e.Request.Method,
		Path: path, Status: e.Status, MS: e.Duration * 1000, Bytes: e.Size,
	}, true
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
	l.mu.Unlock()
}

// Flush writes the requests waiting in memory.
func (l *Log) Flush(ctx context.Context) error {
	l.mu.Lock()
	batch := l.pending
	l.pending = nil
	l.mu.Unlock()
	if len(batch) == 0 {
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
			`INSERT INTO requests(project, deployment, ts, method, path, status, ms, bytes) VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
			r.Project, r.Deployment, r.At, r.Method, r.Path, r.Status, r.MS, r.Bytes); err != nil {
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
	if err := tx.Commit(); err != nil {
		requeue()
		return err
	}
	return nil
}

func (l *Log) cleanUp(ctx context.Context) {
	now := time.Now()
	if _, err := l.db.ExecContext(ctx, `DELETE FROM requests WHERE ts < ?`, now.Add(-KeepLines).UnixMilli()); err != nil {
		l.log.Warn("clear old requests", "err", err)
	}
	if _, err := l.db.ExecContext(ctx, `DELETE FROM hours WHERE hour < ?`, now.Add(-KeepHours).UnixMilli()); err != nil {
		l.log.Warn("clear old request counts", "err", err)
	}
}

// Filter narrows the list of requests.
type Filter struct {
	From, To int64  // ms; To 0 means now
	Status   string // "", 2xx, 3xx, 4xx, 5xx or errors
	Query    string // part of the path
	After    int64  // only requests newer than this ID, for following
	Limit    int
	Forward  bool // read the earliest page after a follow cursor
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
	rows, err := l.db.QueryContext(ctx, `SELECT id, deployment, ts, method, path, status, ms, bytes FROM requests WHERE `+where+
		` ORDER BY id`+order+` LIMIT ?`, append(args, f.Limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Line
	for rows.Next() {
		var r Line
		if err := rows.Scan(&r.ID, &r.Deployment, &r.At, &r.Method, &r.Path, &r.Status, &r.MS, &r.Bytes); err != nil {
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
	At         int64 `json:"at"` // ms, the bucket's start
	Requests   int   `json:"requests"`
	Errors     int   `json:"errors"`
	E4xx, E5xx int   `json:"-"`
}

// Chart counts requests and errors over [from, to) in buckets: minutes for
// two hours or less (from the lines), hours up to eight days, days beyond
// (from the hourly counts, which are kept 6 months).
func (l *Log) Chart(ctx context.Context, project string, from, to int64, status, query string) ([]Bucket, int64, error) {
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
	step := size.Milliseconds()
	start := from - from%step
	n := int((to - start + step - 1) / step)
	buckets := make([]Bucket, n)
	for i := range buckets {
		buckets[i].At = start + int64(i)*step
	}
	// Whole hours can use retained counts. At a recent range boundary we
	// still have individual timestamps, so count only the part of that hour
	// inside the range (local midnight need not fall on a UTC hour).
	where, args := Filter{From: from, To: to, Status: status, Query: query}.where(project)
	linesSQL := `SELECT ts - ts % ? AS bucket, count(*), sum(status BETWEEN 400 AND 499), sum(status >= 500)
		FROM requests WHERE ` + where
	args = append([]any{step}, args...)
	if size != time.Minute && status == "" && strings.TrimSpace(query) == "" {
		hour := time.Hour.Milliseconds()
		first, last := from-from%hour, to
		cutoff := time.Now().Add(-KeepLines).UnixMilli()
		if first < from && first >= cutoff {
			first += hour
		}
		if to-to%hour >= cutoff {
			last = to - to%hour
		}
		linesSQL += ` AND (ts < ? OR ts >= ?) GROUP BY bucket
			UNION ALL SELECT hour - hour % ? AS bucket, sum(requests), sum(e4xx), sum(e5xx)
			FROM hours WHERE project = ? AND hour >= ? AND hour < ?`
		args = append(args, first, last, step, project, first, last)
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
		idx := int((at - start) / step)
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
