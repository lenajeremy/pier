package database

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"pier/internal/store"
)

// The dashboard's Data and Query read a database through pierd, which
// connects to the server's loopback port as the database's own role: app
// for production, and a preview's own role for a preview. Browsing runs in
// a read-only transaction; Query runs what it's given.

// MaxValue is how much of one value is sent, in characters.
const MaxValue = 10_000

// MaxRows is how many rows of a query's result are sent.
const MaxRows = 500

// QueryTimeout is how long one statement of a query may run.
const QueryTimeout = 30 * time.Second

// Column is a column of a table or a result: its name and its type, as
// Postgres names it (int4, text, timestamptz).
type Column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// Table is a table or view in a database.
type Table struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`  // table, view or materialized view
	Rows   int64  `json:"rows"`  // counted, up to CountUpTo
	More   bool   `json:"more"`  // there are more than CountUpTo
	Bytes  int64  `json:"bytes"` // on disk, with its indexes
}

// CountUpTo is the most rows Pier counts in one table; past it, a table
// shows "more than".
const CountUpTo = 100_000

// Result is one statement's result.
type Result struct {
	Columns   []Column    `json:"columns"`
	Rows      [][]*string `json:"rows"`
	Truncated bool        `json:"truncated,omitempty"` // more rows than MaxRows came back
	Tag       string      `json:"tag"`                 // what Postgres said it did: SELECT 4, UPDATE 2
}

// Page is a page of a table's rows.
type Page struct {
	Table   Table       `json:"table"`
	Columns []Column    `json:"columns"`
	Rows    [][]*string `json:"rows"`
	Offset  int         `json:"offset"`
	Limit   int         `json:"limit"`
	Sort    string      `json:"sort,omitempty"`
	Desc    bool        `json:"desc,omitempty"`
	Where   string      `json:"where,omitempty"`
	Key     []string    `json:"key,omitempty"` // the primary key's columns
	// Refs are the columns that are foreign keys, and the column each
	// points to.
	Refs map[string]Ref `json:"refs"`
	// Count is how many rows match, counted up to CountUpTo; More says
	// there are more than that.
	Count int64 `json:"count"`
	More  bool  `json:"more"`
	MS    int64 `json:"ms"`
}

// Ref is where a foreign key points.
type Ref struct {
	Schema string `json:"schema"`
	Table  string `json:"table"`
	Column string `json:"column"`
}

// QueryError is why a query failed, as Postgres put it.
type QueryError struct {
	Message  string `json:"message"`
	Detail   string `json:"detail,omitempty"`
	Hint     string `json:"hint,omitempty"`
	Position int    `json:"position,omitempty"` // where in the SQL, counting from 1
}

func (e *QueryError) Error() string { return e.Message }

// target names one of a project's databases, by name; empty is the one
// production uses, or the first.
func (m *Manager) target(ctx context.Context, project, db string) (role, pass string, err error) {
	if _, err := m.Store.Database(ctx, project); errors.Is(err, store.ErrNotFound) {
		return "", "", ErrNone
	} else if err != nil {
		return "", "", err
	}
	d, err := m.pick(ctx, project, db)
	if err != nil {
		return "", "", err
	}
	pass, err = m.Box.Open(d.Password, binding(project, d.Name))
	return d.Name, pass, err
}

// pick finds a database by name, or with name empty the one production
// uses, or the first.
func (m *Manager) pick(ctx context.Context, project, name string) (store.ProjectDatabase, error) {
	all, err := m.Store.ProjectDatabases(ctx, project)
	if err != nil {
		return store.ProjectDatabase{}, err
	}
	for _, d := range all {
		if d.Name == name {
			return d, nil
		}
	}
	if name == "" && len(all) > 0 {
		for _, d := range all {
			if d.Serves(store.TargetProduction) {
				return d, nil
			}
		}
		return all[0], nil
	}
	return store.ProjectDatabase{}, fmt.Errorf("there's no database called %s", name)
}

// LocalURL is a database's address on this machine's loopback port, for a
// tunnel or for pierd itself.
func (m *Manager) LocalURL(ctx context.Context, project, db, host string) (string, error) {
	role, pass, err := m.target(ctx, project, db)
	if err != nil {
		return "", err
	}
	u := url.URL{Scheme: "postgres", User: url.UserPassword(role, pass), Host: host, Path: "/" + role, RawQuery: "sslmode=disable"}
	return u.String(), nil
}

// connect opens a connection to one of a project's databases as its role.
func (m *Manager) connect(ctx context.Context, project, db string) (*pgconn.PgConn, error) {
	addr, err := m.Host.PostgresAddress(ctx, project)
	if err != nil {
		return nil, err
	}
	u, err := m.LocalURL(ctx, project, db, addr)
	if err != nil {
		return nil, err
	}
	cfg, err := pgconn.ParseConfig(u + "&connect_timeout=5&application_name=pier")
	if err != nil {
		return nil, err
	}
	conn, err := pgconn.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("couldn't connect to the database: %w", err)
	}
	return conn, nil
}

// run runs SQL in the simple protocol, which allows several statements, and
// collects each statement's result.
func run(ctx context.Context, conn *pgconn.PgConn, sql string, maxRows int) ([]Result, error) {
	var out []Result
	mrr := conn.Exec(ctx, sql)
	oids := map[uint32]bool{}
	for mrr.NextResult() {
		rr := mrr.ResultReader()
		res := Result{Rows: [][]*string{}}
		fields := rr.FieldDescriptions()
		for _, f := range fields {
			res.Columns = append(res.Columns, Column{Name: f.Name, Type: strconv.FormatUint(uint64(f.DataTypeOID), 10)})
			oids[f.DataTypeOID] = true
		}
		for rr.NextRow() {
			if len(res.Rows) >= maxRows {
				res.Truncated = true
				continue
			}
			row := make([]*string, len(fields))
			for i, v := range rr.Values() {
				if v == nil {
					continue
				}
				s := value(v, fields[i].DataTypeOID)
				row[i] = &s
			}
			res.Rows = append(res.Rows, row)
		}
		tag, err := rr.Close()
		if err != nil {
			mrr.Close()
			return out, err
		}
		res.Tag = tag.String()
		out = append(out, res)
	}
	if err := mrr.Close(); err != nil {
		return out, err
	}
	if len(oids) > 0 {
		names, err := typeNames(ctx, conn, oids)
		if err == nil {
			for i := range out {
				for j := range out[i].Columns {
					oid, _ := strconv.ParseUint(out[i].Columns[j].Type, 10, 32)
					out[i].Columns[j].Type = names[uint32(oid)]
				}
			}
		}
	}
	return out, nil
}

// byteaOID is Postgres's bytea type, which comes as hex: \x89504e47...
const byteaOID = 17

// value is one value as it's sent: clipped to MaxValue characters, and a
// long bytea cut to its first 64 bytes with its whole size after them, as
// …(34538 bytes), since a browser has no use for the rest as hex.
func value(v []byte, oid uint32) string {
	if oid == byteaOID && len(v) > 2+128 {
		return string(v[:2+128]) + fmt.Sprintf("…(%d bytes)", (len(v)-2)/2)
	}
	return clip(string(v))
}

// clip keeps the first MaxValue characters of a value.
func clip(s string) string {
	if len(s) <= MaxValue {
		return s
	}
	n := 0
	for i := range s {
		if n == MaxValue {
			return s[:i] + "…"
		}
		n++
	}
	return s
}

// typeNames looks up the names of the types a result used.
func typeNames(ctx context.Context, conn *pgconn.PgConn, oids map[uint32]bool) (map[uint32]string, error) {
	var list []string
	for oid := range oids {
		list = append(list, strconv.FormatUint(uint64(oid), 10))
	}
	res, err := run1(ctx, conn, `SELECT oid::int8, typname FROM pg_catalog.pg_type WHERE oid IN (`+strings.Join(list, ",")+`)`)
	if err != nil {
		return nil, err
	}
	out := map[uint32]string{}
	for _, row := range res.Rows {
		if row[0] != nil && row[1] != nil {
			oid, _ := strconv.ParseUint(*row[0], 10, 32)
			out[uint32(oid)] = *row[1]
		}
	}
	return out, nil
}

// run1 runs one statement whose types don't need names.
func run1(ctx context.Context, conn *pgconn.PgConn, sql string) (Result, error) {
	rr := conn.ExecParams(ctx, sql, nil, nil, nil, nil)
	res := Result{Rows: [][]*string{}}
	for rr.NextRow() {
		row := make([]*string, len(rr.FieldDescriptions()))
		for i, v := range rr.Values() {
			if v != nil {
				s := string(v)
				row[i] = &s
			}
		}
		res.Rows = append(res.Rows, row)
	}
	_, err := rr.Close()
	return res, err
}

func queryError(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return &QueryError{Message: pg.Message, Detail: pg.Detail, Hint: pg.Hint, Position: int(pg.Position)}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &QueryError{Message: "It took too long, so it was stopped."}
	}
	return err
}

func quote(schema, name string) string { return pgx.Identifier{schema, name}.Sanitize() }

func literal(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// Tables lists the tables and views in one of a project's databases, with
// how many rows each has, counted up to CountUpTo.
func (m *Manager) Tables(ctx context.Context, project, db string) ([]Table, error) {
	conn, err := m.connect(ctx, project, db)
	if err != nil {
		return nil, err
	}
	defer conn.Close(context.WithoutCancel(ctx))
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	res, err := run1(ctx, conn, `SELECT n.nspname, c.relname,
		CASE c.relkind WHEN 'v' THEN 'view' WHEN 'm' THEN 'materialized view' ELSE 'table' END,
		pg_total_relation_size(c.oid)
		FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind IN ('r','p','v','m') AND n.nspname NOT IN ('pg_catalog','information_schema') AND n.nspname NOT LIKE 'pg\_%'
		AND has_table_privilege(c.oid, 'SELECT')
		ORDER BY n.nspname = 'public' DESC, n.nspname, c.relname LIMIT 500`)
	if err != nil {
		return nil, queryError(err)
	}
	tables := []Table{}
	var counts []string
	for _, row := range res.Rows {
		t := Table{Schema: *row[0], Name: *row[1], Kind: *row[2]}
		t.Bytes, _ = strconv.ParseInt(*row[3], 10, 64)
		tables = append(tables, t)
		if t.Kind == "table" {
			counts = append(counts, fmt.Sprintf(`SELECT %d, count(*) FROM (SELECT 1 FROM %s LIMIT %d) s`, len(tables)-1, quote(t.Schema, t.Name), CountUpTo+1))
		}
	}
	if len(counts) > 0 {
		sql := `BEGIN READ ONLY; SET LOCAL statement_timeout = '10s'; ` + strings.Join(counts, "; ") + `; COMMIT`
		if results, err := run(ctx, conn, sql, 1); err == nil {
			for _, r := range results {
				if len(r.Rows) == 1 && len(r.Rows[0]) == 2 && r.Rows[0][0] != nil && r.Rows[0][1] != nil {
					i, _ := strconv.Atoi(*r.Rows[0][0])
					n, _ := strconv.ParseInt(*r.Rows[0][1], 10, 64)
					if i < len(tables) {
						tables[i].Rows, tables[i].More = min(n, CountUpTo), n > CountUpTo
					}
				}
			}
		}
	}
	return tables, nil
}

// Rows reads a page of a table's rows in a read-only transaction: those
// that match where, a filter in SQL, if there is one; sorted by sort when
// it's one of the table's columns, and by the primary key when it isn't
// given. The filter goes into one statement sent on its own, so it can't
// end the transaction or start another statement.
func (m *Manager) Rows(ctx context.Context, project, db, schema, name, sort string, desc bool, where string, offset, limit int) (Page, error) {
	start := time.Now()
	tables, err := m.Tables(ctx, project, db)
	if err != nil {
		return Page{}, err
	}
	i := slices.IndexFunc(tables, func(t Table) bool { return t.Schema == schema && t.Name == name })
	if i < 0 {
		return Page{}, fmt.Errorf("there's no table %s.%s", schema, name)
	}
	page := Page{Table: tables[i], Offset: max(offset, 0), Limit: min(max(limit, 1), 200), Rows: [][]*string{}, Refs: map[string]Ref{},
		Where: strings.TrimSpace(where)}
	conn, err := m.connect(ctx, project, db)
	if err != nil {
		return page, err
	}
	defer conn.Close(context.WithoutCancel(ctx))
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ident := quote(schema, name)
	meta, err := run1(ctx, conn, `SELECT a.attname, COALESCE(a.attnum = ANY(i.indkey), false) FROM pg_catalog.pg_attribute a
		LEFT JOIN pg_catalog.pg_index i ON i.indrelid = a.attrelid AND i.indisprimary
		WHERE a.attrelid = `+literal(ident)+`::regclass AND a.attnum > 0 AND NOT a.attisdropped ORDER BY a.attnum`)
	if err != nil {
		return page, queryError(err)
	}
	var cols []string
	for _, row := range meta.Rows {
		cols = append(cols, *row[0])
		if *row[1] == "t" {
			page.Key = append(page.Key, *row[0])
		}
	}
	// Single-column foreign keys, so a value can link to its row.
	refs, err := run1(ctx, conn, `SELECT a.attname, fn.nspname, fc.relname, fa.attname FROM pg_catalog.pg_constraint c
		JOIN pg_catalog.pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = c.conkey[1]
		JOIN pg_catalog.pg_class fc ON fc.oid = c.confrelid
		JOIN pg_catalog.pg_namespace fn ON fn.oid = fc.relnamespace
		JOIN pg_catalog.pg_attribute fa ON fa.attrelid = c.confrelid AND fa.attnum = c.confkey[1]
		WHERE c.contype = 'f' AND c.conrelid = `+literal(ident)+`::regclass AND array_length(c.conkey, 1) = 1`)
	if err == nil {
		for _, row := range refs.Rows {
			page.Refs[*row[0]] = Ref{Schema: *row[1], Table: *row[2], Column: *row[3]}
		}
	}
	order := ""
	switch {
	case sort != "" && slices.Contains(cols, sort):
		page.Sort, page.Desc = sort, desc
		dir := "ASC NULLS LAST"
		if desc {
			dir = "DESC NULLS LAST"
		}
		order = " ORDER BY " + pgx.Identifier{sort}.Sanitize() + " " + dir
	case len(page.Key) > 0:
		var keys []string
		for _, k := range page.Key {
			keys = append(keys, pgx.Identifier{k}.Sanitize())
		}
		order = " ORDER BY " + strings.Join(keys, ", ")
	}
	filter := ""
	if page.Where != "" {
		filter = " WHERE (" + page.Where + ")"
	}
	if _, err := run(ctx, conn, `BEGIN READ ONLY; SET LOCAL statement_timeout = '15s'`, 0); err != nil {
		return page, queryError(err)
	}
	defer run(context.WithoutCancel(ctx), conn, `ROLLBACK`, 0)
	res, err := params(ctx, conn, fmt.Sprintf(`SELECT * FROM %s%s%s LIMIT %d OFFSET %d`, ident, filter, order, page.Limit, page.Offset), page.Limit)
	if err != nil {
		return page, queryError(err)
	}
	page.Columns, page.Rows = res.Columns, res.Rows
	if count, err := params(ctx, conn, fmt.Sprintf(`SELECT count(*) FROM (SELECT 1 FROM %s%s LIMIT %d) s`, ident, filter, CountUpTo+1), 1); err == nil && len(count.Rows) == 1 && count.Rows[0][0] != nil {
		n, _ := strconv.ParseInt(*count.Rows[0][0], 10, 64)
		page.Count, page.More = min(n, CountUpTo), n > CountUpTo
	}
	page.MS = time.Since(start).Milliseconds()
	return page, nil
}

// params runs one statement in the extended protocol, which refuses more
// than one, and names its columns' types.
func params(ctx context.Context, conn *pgconn.PgConn, sql string, maxRows int) (Result, error) {
	rr := conn.ExecParams(ctx, sql, nil, nil, nil, nil)
	res := Result{Rows: [][]*string{}}
	fields := rr.FieldDescriptions()
	oids := map[uint32]bool{}
	for _, f := range fields {
		res.Columns = append(res.Columns, Column{Name: f.Name, Type: strconv.FormatUint(uint64(f.DataTypeOID), 10)})
		oids[f.DataTypeOID] = true
	}
	for rr.NextRow() {
		if len(res.Rows) >= maxRows {
			res.Truncated = true
			continue
		}
		row := make([]*string, len(fields))
		for i, v := range rr.Values() {
			if v != nil {
				s := value(v, fields[i].DataTypeOID)
				row[i] = &s
			}
		}
		res.Rows = append(res.Rows, row)
	}
	tag, err := rr.Close()
	if err != nil {
		return res, err
	}
	res.Tag = tag.String()
	if len(oids) > 0 {
		if names, err := typeNames(ctx, conn, oids); err == nil {
			for j := range res.Columns {
				oid, _ := strconv.ParseUint(res.Columns[j].Type, 10, 32)
				res.Columns[j].Type = names[uint32(oid)]
			}
		}
	}
	return res, nil
}

// QueryOutcome is what running a query gave: each statement's result, or
// the error that stopped it, and how long it took.
type QueryOutcome struct {
	Results []Result    `json:"results"`
	Error   *QueryError `json:"error,omitempty"`
	MS      int64       `json:"ms"`
}

// Query runs SQL in one of a project's databases as its role. Its
// statements run together in one transaction, unless the SQL commits on
// its own, so an error part way changes nothing. Each may run QueryTimeout.
func (m *Manager) Query(ctx context.Context, project, db, sql, by string) (QueryOutcome, error) {
	conn, err := m.connect(ctx, project, db)
	if err != nil {
		return QueryOutcome{}, err
	}
	defer conn.Close(context.WithoutCancel(ctx))
	start := time.Now()
	if _, err := run(ctx, conn, fmt.Sprintf(`SET statement_timeout = '%ds'`, int(QueryTimeout.Seconds())), 0); err != nil {
		return QueryOutcome{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, QueryTimeout+10*time.Second)
	defer cancel()
	results, err := run(ctx, conn, sql, MaxRows)
	out := QueryOutcome{Results: results, MS: time.Since(start).Milliseconds()}
	if out.Results == nil {
		out.Results = []Result{}
	}
	if err != nil {
		var qe *QueryError
		if errors.As(queryError(err), &qe) {
			out.Error = qe
		} else {
			return out, err
		}
	}
	// The activity says what the query did, not the SQL, which can hold
	// values viewers mustn't see.
	var did []string
	for _, r := range out.Results {
		if r.Tag != "" {
			did = append(did, r.Tag)
		}
	}
	if out.Error != nil {
		did = append(did, "stopped by an error")
	}
	if d, err := m.pick(ctx, project, db); err == nil {
		db = d.Name
	}
	m.Store.RecordEvent(context.WithoutCancel(ctx), project, "Ran a query on "+db, strings.Join(did, ", "), "", by)
	return out, nil
}
