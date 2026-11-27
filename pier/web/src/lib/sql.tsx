import type { ReactNode } from 'react'

// How the dashboard shows a database's values and SQL: each value by its
// type (times split into date and time, numbers aligned right, JSON with
// its keys dimmed), and SQL coloured by keyword, function and string.

const numeric = new Set(['int2', 'int4', 'int8', 'float4', 'float8', 'numeric', 'money', 'oid'])
const times = new Set(['timestamptz', 'timestamp', 'date', 'time', 'timetz'])

export const isNumeric = (type: string) => numeric.has(type)

// Value is one cell's value in a grid, in one colour: NULL is dimmed, a
// time is cut to the second, and the full value is in the row's panel.
export function Value({ value, type }: { value: string | null; type: string }) {
  if (value === null) return <span className="v-null">NULL</span>
  if (type === 'bool') return <>{value === 't' ? 'true' : 'false'}</>
  if (times.has(type)) return <>{value.replace(/\.\d+/, '').replace(/\+00$/, '')}</>
  return <>{value.length > 300 ? value.slice(0, 300) : value}</>
}

// json colours a JSON value on one line: keys dimmed, punctuation faint.
export function json(text: string): ReactNode[] {
  const out: ReactNode[] = []
  const re = /("(?:[^"\\]|\\.)*")(\s*:)?|([{}[\],:])|([^"{}[\],:]+)/g
  let m: RegExpExecArray | null
  let i = 0
  while ((m = re.exec(text))) {
    if (m[1] && m[2]) {
      out.push(<span key={i++} className="v-dim">{m[1]}</span>, <span key={i++} className="v-faint">{m[2]}</span>)
    } else if (m[1]) {
      out.push(<span key={i++}>{m[1]}</span>)
    } else if (m[3]) {
      out.push(<span key={i++} className="v-faint">{m[3]}</span>)
    } else {
      out.push(<span key={i++}>{m[4]}</span>)
    }
  }
  return out
}

// pretty indents a JSON value, or gives it back as it is if it isn't JSON.
export function pretty(text: string): string {
  try {
    return JSON.stringify(JSON.parse(text), null, 2)
  } catch {
    return text
  }
}

const keywords = new Set(
  (
    'select from where and or not in is null as on join left right inner outer full cross group by order having limit offset ' +
    'insert into values update set delete returning create table index unique primary key references foreign drop alter add ' +
    'column constraint default if exists distinct union all except intersect case when then else end with recursive asc desc ' +
    'nulls first last like ilike between true false begin commit rollback grant revoke to cascade interval using view ' +
    'materialized schema extension truncate explain analyze vacuum serial bigserial text integer bigint boolean timestamptz ' +
    'timestamp date jsonb json uuid varchar numeric real double precision any some over partition window filter lateral'
  ).split(' '),
)

// Highlight colours SQL for the editor. It returns the same characters it's
// given, so the coloured copy lines up under the text being typed.
export function Highlight({ sql }: { sql: string }) {
  const out: ReactNode[] = []
  const re = /(--[^\n]*)|('(?:[^']|'')*'?)|("(?:[^"]|"")*"?)|(\b\d+(?:\.\d+)?\b)|([A-Za-z_][A-Za-z0-9_$]*)(?=\s*\()|([A-Za-z_][A-Za-z0-9_$]*)|(\s+|.)/g
  let m: RegExpExecArray | null
  let i = 0
  while ((m = re.exec(sql))) {
    const [all, comment, str, quoted, num, fn, word] = m
    if (comment) out.push(<span key={i++} className="sql-comment">{comment}</span>)
    else if (str) out.push(<span key={i++} className="sql-string">{str}</span>)
    else if (quoted) out.push(<span key={i++}>{quoted}</span>)
    else if (num) out.push(<span key={i++} className="sql-number">{num}</span>)
    else if (fn) out.push(<span key={i++} className={keywords.has(fn.toLowerCase()) ? 'sql-keyword' : 'sql-function'}>{fn}</span>)
    else if (word) out.push(keywords.has(word.toLowerCase()) ? <span key={i++} className="sql-keyword">{word}</span> : word)
    else out.push(all)
  }
  return <>{out}</>
}

// csv writes rows as CSV, with a header row.
export function csv(columns: string[], rows: (string | null)[][]): string {
  const cell = (v: string | null) => (v === null ? '' : /[",\n\r]/.test(v) ? `"${v.replace(/"/g, '""')}"` : v)
  return [columns.map(cell).join(','), ...rows.map((r) => r.map(cell).join(','))].join('\n')
}

// lineOf is the line, from 1, that a character position (from 1) is on.
export function lineOf(sql: string, position: number): number {
  return sql.slice(0, Math.max(0, position - 1)).split('\n').length
}
