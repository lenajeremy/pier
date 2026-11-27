import { useEffect, useRef, useState } from 'react'
import { api, ApiError, type DbOutcome, type DbResult } from '../lib/api'
import { ago } from '../lib/format'
import { csv, Highlight, isNumeric, lineOf, Value } from '../lib/sql'
import { Spinner } from '../components/Spinner'
import { copy } from '../components/Toast'
import { databasePath } from './Database'
import { SideHandle, useSideWidth } from './DatabaseData'

// A query someone ran, kept in their browser: the SQL, which database,
// and what it did.
type Ran = { sql: string; db: string; at: string; did: string; failed?: boolean }

const KEEP = 50
const storeKey = (project: string) => `pier.queries.${project}`

function history(project: string): Ran[] {
  try {
    return JSON.parse(localStorage.getItem(storeKey(project)) ?? '[]')
  } catch {
    return []
  }
}

function remember(project: string, r: Ran) {
  try {
    const all = [r, ...history(project).filter((h) => h.sql !== r.sql || h.db !== r.db)].slice(0, KEEP)
    localStorage.setItem(storeKey(project), JSON.stringify(all))
  } catch {
    // Private windows keep no history.
  }
}

// did says what a query did, in a few words, for the history.
function did(out: DbOutcome): string {
  if (out.error) return 'failed'
  const last = out.results[out.results.length - 1]
  if (!last) return 'done'
  if (last.columns) return last.rows.length === 1 ? '1 row' : `${last.rows.length.toLocaleString('en-GB')}${last.truncated ? '+' : ''} rows`
  return changed(last.tag)
}

// changed turns a command tag (UPDATE 2) into words.
function changed(tag: string): string {
  const words = tag.split(' ')
  const n = Number(words[words.length - 1])
  const rows = (k: number) => (k === 1 ? '1 row' : `${k.toLocaleString('en-GB')} rows`)
  if (words.length < 2 || Number.isNaN(n)) return tag ? tag.charAt(0) + tag.slice(1).toLowerCase() : 'done'
  switch (words[0]) {
    case 'INSERT':
      return `${rows(n)} added`
    case 'DELETE':
      return `${rows(n)} deleted`
    case 'UPDATE':
    case 'MERGE':
      return `${rows(n)} changed`
  }
  return tag.charAt(0) + tag.slice(1).toLowerCase()
}

// QueryView is the Database tab's Query: an editor, the result of what ran,
// and the queries this person ran before.
export function QueryView({ project, db, role, draft }: { project: string; db: string; role: string; draft?: string }) {
  const side = useSideWidth()
  const [sql, setSql] = useState(draft ?? '')
  const [ran, setRan] = useState<Ran[]>(() => history(project))
  const [out, setOut] = useState<{ sql: string; outcome: DbOutcome } | null>(null)
  const [busy, setBusy] = useState(false)
  const [problem, setProblem] = useState('')
  const area = useRef<HTMLTextAreaElement>(null)
  const shade = useRef<HTMLPreElement>(null)

  useEffect(() => {
    if (draft) setSql(draft)
  }, [draft])

  const run = async () => {
    if (!sql.trim() || busy) return
    setBusy(true)
    setProblem('')
    try {
      const outcome = await api<DbOutcome>(`${databasePath(project)}/query`, { method: 'POST', body: JSON.stringify({ db, sql }) })
      setOut({ sql, outcome })
      const r: Ran = { sql, db, at: new Date().toISOString(), did: did(outcome), failed: !!outcome.error }
      remember(project, r)
      setRan(history(project))
    } catch (e) {
      setProblem(e instanceof ApiError ? e.message : "Can't reach pierd.")
    } finally {
      setBusy(false)
    }
  }

  const lines = sql.split('\n').length
  const err = out?.outcome.error
  const errLine = err?.position ? lineOf(out!.sql, err.position) : null
  // Postgres's hint names the column it thinks was meant.
  const suggestion = err?.hint ? /Perhaps you meant to reference the column "([^"]+)"/.exec(err.hint)?.[1] : undefined
  const wrong = err ? /column "?([^"\s]+)"? does not exist/.exec(err.message)?.[1] : undefined
  const fix = () => {
    if (!suggestion || !wrong || !err?.position) return
    const at = err.position - 1
    const src = out!.sql
    const next = src.slice(0, at) + src.slice(at).replace(wrong, suggestion)
    setSql(next)
    area.current?.focus()
  }

  return (
    <div className="db-browser" style={{ ['--db-side' as string]: `${side.width}px` }}>
      <SideHandle side={side} />
      <div className="panel db-history">
        <div className="db-history-head">
          <span className="t-small t-strong">History</span>
          {ran.length > 0 && <span className="t-note">Yours</span>}
        </div>
        {ran.length === 0 && <span className="t-note db-none">Queries you run show up here.</span>}
        {ran.map((r) => (
          <button key={r.at} className={'db-ran' + (out?.sql === r.sql ? ' on' : '')} onClick={() => (setSql(r.sql), area.current?.focus())}>
            <span className="mono db-ran-sql">{r.sql.replace(/^\s*--[^\n]*\n/, '').trim().split('\n')[0]}</span>
            <span className={'t-note' + (r.failed ? ' db-ran-failed' : '')}>
              {r.failed && <i className="swatch failed" aria-hidden />}
              {ago(r.at).replace(/^./, (c) => c.toUpperCase())}, {r.did}
            </span>
          </button>
        ))}
      </div>
      <div className="db-work">
        <div className="panel db-editor-panel">
          <div className="db-editor">
            <div className="db-lines mono" aria-hidden>
              {Array.from({ length: Math.max(lines, 6) }, (_, i) => (
                <span key={i} className={errLine === i + 1 ? 'bad' : ''}>
                  {i + 1}
                </span>
              ))}
            </div>
            <div className="db-code">
              <pre ref={shade} className="db-shade mono" aria-hidden>
                <Highlight sql={sql} />
                {'\n'}
              </pre>
              <textarea
                ref={area}
                className="db-textarea mono"
                value={sql}
                onChange={(e) => setSql(e.target.value)}
                onScroll={(e) => {
                  if (shade.current) shade.current.scrollLeft = e.currentTarget.scrollLeft
                }}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
                    e.preventDefault()
                    run()
                  }
                  if (e.key === 'Tab' && !e.shiftKey) {
                    e.preventDefault()
                    const t = e.currentTarget
                    const s = t.selectionStart
                    setSql(sql.slice(0, s) + '  ' + sql.slice(t.selectionEnd))
                    requestAnimationFrame(() => t.setSelectionRange(s + 2, s + 2))
                  }
                }}
                placeholder="SELECT * FROM users LIMIT 10"
                spellCheck={false}
                autoCapitalize="off"
                autoCorrect="off"
                aria-label="SQL"
                rows={Math.max(lines, 6)}
              />
            </div>
          </div>
          <div className="db-editor-foot">
            <span className="t-note grow">
              Runs as <span className="mono t-muted">{role}</span>, so it can change data.
            </span>
            {problem && <span className="field-message">{problem}</span>}
            <button className="btn primary db-run" disabled={busy || !sql.trim()} onClick={run}>
              {busy && <Spinner />}
              Run
              <kbd>⌘↵</kbd>
            </button>
          </div>
        </div>
        {err ? (
          <div className="panel db-error" role="alert">
            <div className="db-error-head">
              <i className="swatch failed" aria-hidden />
              <span className="t-small t-strong grow">{errLine ? `Postgres stopped at line ${errLine}` : 'Postgres stopped'}</span>
              <span className="t-note">Nothing changed</span>
            </div>
            <span className="mono db-error-message">{err.message}</span>
            {err.detail && <span className="t-caption t-muted">{err.detail}</span>}
            {(suggestion || (err.hint && !suggestion)) && (
              <div className="db-error-hint">
                <span className="t-caption t-muted grow">
                  {suggestion ? (
                    <>
                      Postgres suggests <span className="mono">{suggestion}</span>.
                    </>
                  ) : (
                    err.hint
                  )}
                </span>
                {suggestion && wrong && (
                  <button className="btn quiet" onClick={fix}>
                    Use {suggestion}
                  </button>
                )}
              </div>
            )}
          </div>
        ) : (
          out && <Results outcome={out.outcome} />
        )}
      </div>
    </div>
  )
}

function Results({ outcome }: { outcome: DbOutcome }) {
  const sets = outcome.results.filter((r) => r.columns)
  const [at, setAt] = useState(0)
  useEffect(() => setAt(Math.max(0, sets.length - 1)), [outcome]) // eslint-disable-line react-hooks/exhaustive-deps
  const shown: DbResult | undefined = sets[at]
  const writes = outcome.results.filter((r) => !r.columns && r.tag && !/^(BEGIN|COMMIT|SET|ROLLBACK)/.test(r.tag))
  if (!shown) {
    return (
      <div className="panel db-result-head only">
        <i className="swatch done" aria-hidden />
        <span className="t-small t-strong">{writes.length ? writes.map((w) => changed(w.tag)).join(', ') : 'Done'}</span>
        <span className="t-note">in {outcome.ms.toLocaleString('en-GB')} ms</span>
      </div>
    )
  }
  const cols = shown.columns!
  // A number column gets a bar for each value, so they compare at a glance.
  const bars = cols.map((c, i) => {
    if (!isNumeric(c.type) || shown.rows.length < 2) return 0
    const values = shown.rows.map((r) => Number(r[i])).filter((n) => Number.isFinite(n))
    if (values.some((n) => n < 0)) return 0
    return Math.max(...values, 0)
  })
  return (
    <div className="panel db-result">
      <div className="db-result-head">
        <i className="swatch done" aria-hidden />
        <span className="t-small t-strong">
          {shown.rows.length === 1 ? '1 row' : `${shown.rows.length.toLocaleString('en-GB')} rows`}
          {shown.truncated ? ', the first 500' : ''}
        </span>
        <span className="t-note grow">in {outcome.ms.toLocaleString('en-GB')} ms</span>
        {sets.length > 1 && (
          <div className="seg small" role="tablist" aria-label="Results">
            {sets.map((_, i) => (
              <button key={i} role="tab" aria-selected={i === at} className={i === at ? 'on' : ''} onClick={() => setAt(i)}>
                {i + 1}
              </button>
            ))}
          </div>
        )}
        <button className="btn quiet" onClick={() => copy(csv(cols.map((c) => c.name), shown.rows), 'the result as CSV')}>
          Copy as CSV
        </button>
      </div>
      <div className="db-grid-scroll">
        <table className="db-grid">
          <thead>
            <tr>
              {cols.map((c) => (
                <th key={c.name} className={isNumeric(c.type) ? 'num' : ''}>
                  <span data-tip={c.type} data-tip-mono>
                    {c.name}
                  </span>
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {shown.rows.map((row, i) => (
              <tr key={i}>
                {row.map((v, j) =>
                  bars[j] > 0 ? (
                    <td key={j} className="num bar-cell">
                      <span className="db-bar-value">
                        <Value value={v} type={cols[j].type} />
                      </span>
                      <span className="db-bar">
                        <i style={{ width: `${v === null ? 0 : Math.max(1, (Number(v) / bars[j]) * 100)}%` }} />
                      </span>
                    </td>
                  ) : (
                    <td key={j} className={isNumeric(cols[j].type) ? 'num' : ''}>
                      <Value value={v} type={cols[j].type} />
                    </td>
                  ),
                )}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
