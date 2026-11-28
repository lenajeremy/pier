import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { api } from '../lib/api'
import { timeZone, utcOffset } from '../lib/calendar'
import { Calendar, Close, Copy, Disclosure, Search } from '../components/Icons'
import { Menu } from '../components/Menu'
import { Problem, Skeleton } from '../components/ui'
import { copy } from '../components/Toast'

// LogExplorer is every request a project's deployments answered: fields on
// the left with their commonest values, filters on top, a chart that stacks
// server errors, client errors and the rest, and the lines themselves, the
// newest first, each opening to everything Pier knows about it.

type Line = {
  id: number
  deployment: string
  at: number
  method: string
  path: string
  status: number
  ms: number
  bytes: number
  host?: string
  env?: string
  page?: boolean
  bot?: string
  browser?: string
  device?: string
  referrer?: string
  country?: string
  trace?: string // the trace Pier's router started, when traces are on
}
type Bucket = { at: number; requests: number; errors: number; e4xx: number; e5xx: number }
type Chart = { buckets: Bucket[]; bucket_ms: number }
type Match = { field: string; value: string; not?: boolean }

const ranges = [
  { key: '15m', label: 'Last 15 minutes', ms: 15 * 60_000 },
  { key: '1h', label: 'Last hour', ms: 3_600_000 },
  { key: '24h', label: 'Last 24 hours', ms: 86_400_000 },
  { key: '7d', label: 'Last 7 days', ms: 7 * 86_400_000 },
]
const groups: [string, [string, string][]][] = [
  ['Request', [['status', 'status'], ['method', 'method'], ['path', 'path'], ['host', 'host']]],
  ['Visitor', [['browser', 'browser'], ['device', 'device'], ['referrer', 'referrer'], ['country', 'country']]],
  ['Deployment', [['env', 'environment'], ['deployment', 'deployment']]],
  ['Bots', [['bot', 'bot']]],
]
const fieldLabel = Object.fromEntries(groups.flatMap(([, fs]) => fs))

export function LogExplorer({ project, head, onTrace }: { project: string; head: ReactNode; onTrace?: (id: string) => void }) {
  const [range, setRange] = useState(ranges[1])
  const [zoom, setZoom] = useState<{ from: number; to: number } | null>(null)
  const [matches, setMatches] = useState<Match[]>(() => {
    const p = new URLSearchParams(location.search)
    return p.get('status') === 'errors' ? [{ field: 'level', value: 'error' }] : []
  })
  const [typed, setTyped] = useState('')
  const [q, setQ] = useState('')
  const [chart, setChart] = useState<Chart | null>(null)
  const [lines, setLines] = useState<Line[] | null>(null)
  const [failed, setFailed] = useState(false)
  const [open, setOpen] = useState<number | null>(null)
  const [field, setField] = useState<string | null>('status')
  const [fieldSearch, setFieldSearch] = useState('')
  const live = zoom === null

  useEffect(() => {
    const t = window.setTimeout(() => setQ(typed), 300)
    return () => window.clearTimeout(t)
  }, [typed])

  const base = `/api/projects/${encodeURIComponent(project)}/requests`
  const params = (from: number, to: number) => {
    const p = new URLSearchParams({ from: String(from), to: String(to) })
    if (q) p.set('q', q)
    for (const m of matches) p.append(m.not ? 'nf' : 'f', `${m.field}:${m.value}`)
    return p
  }
  const bounds = () => (zoom ? zoom : { from: Date.now() - range.ms, to: Date.now() })

  useEffect(() => {
    let stopped = false
    let timer: number | undefined
    let after = 0
    let all: Line[] = []
    setLines(null)
    setChart(null)
    setFailed(false)
    setOpen(null)
    const tick = async (first: boolean) => {
      const { from, to } = bounds()
      const p = params(from, to)
      try {
        const [c, l] = await Promise.all([
          api<Chart>(`${base}/chart?${p}&tz=${utcOffset(from)}&zone=${encodeURIComponent(timeZone())}`),
          api<{ lines: Line[] }>(`${base}?${p}&limit=300${after ? `&after=${after}` : ''}`),
        ])
        if (stopped) return
        setChart(c)
        const fresh = [...l.lines].reverse()
        all = (first ? fresh : [...fresh, ...all]).filter((x) => x.at >= from).slice(0, 500)
        if (all.length) after = Math.max(after, all[0].id)
        setLines(all)
        setFailed(false)
      } catch {
        if (!stopped) setFailed(true)
      }
      if (!stopped && live) timer = window.setTimeout(() => tick(false), 3000)
    }
    tick(true)
    return () => {
      stopped = true
      window.clearTimeout(timer)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [project, range, zoom, q, JSON.stringify(matches)])

  const add = (m: Match) => {
    setMatches((ms) => [...ms.filter((x) => !(x.field === m.field && x.value === m.value)), m])
  }
  const remove = (i: number) => setMatches((ms) => ms.filter((_, j) => j !== i))
  const totals = useMemo(() => {
    const b = chart?.buckets ?? []
    const e5 = b.reduce((n, x) => n + x.e5xx, 0)
    const e4 = b.reduce((n, x) => n + x.e4xx, 0)
    return { e5, e4, rest: b.reduce((n, x) => n + x.requests, 0) - e5 - e4 }
  }, [chart])

  const fieldsShown = groups
    .map(([g, fs]) => [g, fs.filter(([, label]) => label.includes(fieldSearch.trim().toLowerCase()))] as [string, [string, string][]])
    .filter(([, fs]) => fs.length)

  return (
    <main className="main" style={{ gap: 16 }}>
      {head}
      <div className="le">
        <aside className="panel le-fields">
          <label className="le-input">
            <Search />
            <input value={fieldSearch} onChange={(e) => setFieldSearch(e.target.value)} placeholder="Search fields" aria-label="Search fields" />
          </label>
          {fieldsShown.map(([g, fs]) => (
            <div key={g} className="le-group">
              <div className="le-group-name">{g}</div>
              {fs.map(([f, label]) => (
                <div key={f}>
                  <button className={'le-field' + (field === f ? ' on' : '')} onClick={() => setField(field === f ? null : f)} aria-expanded={field === f}>
                    <span className="mono">{label}</span>
                    <span className={'le-chev' + (field === f ? ' down' : '')}>
                      <Disclosure />
                    </span>
                  </button>
                  {field === f && <FieldValues base={base} params={params(bounds().from, bounds().to)} field={f} onPick={(v) => add({ field: f, value: v })} />}
                </div>
              ))}
            </div>
          ))}
        </aside>
        <div className="le-main">
          <div className="le-toolbar">
            <Menu label="Add a filter" align="start" className="btn quiet medium" items={groups.flatMap(([, fs]) => fs).map(([f, label]) => ({ label, onSelect: () => setField(f) }))}>
              Add filter
            </Menu>
            <label className="le-input grow">
              <Search />
              <input value={typed} onChange={(e) => setTyped(e.target.value)} placeholder="Search paths" aria-label="Search paths" />
            </label>
            <Menu label="Time range" align="end" className="btn quiet medium" items={ranges.map((r) => ({ label: r.label, onSelect: () => { setZoom(null); setRange(r) } }))}>
              <Calendar />
              {range.label}
            </Menu>
          </div>
          {(matches.length > 0 || zoom) && (
            <div className="le-chips">
              {zoom && (
                <span className="le-chip on">
                  time is <b className="mono">{clock(zoom.from)} to {clock(zoom.to)}</b>
                  <button aria-label="Show the whole range" onClick={() => setZoom(null)}>
                    <Close />
                  </button>
                </span>
              )}
              {matches.map((m, i) => (
                <span key={`${m.field}${m.value}${m.not}`} className="le-chip">
                  <button className="le-chip-op" onClick={() => setMatches((ms) => ms.map((x, j) => (j === i ? { ...x, not: !x.not } : x)))} title="Switch between is and isn't">
                    {fieldLabel[m.field] ?? m.field} {m.not ? 'is not' : 'is'}
                  </button>
                  <b className="mono">{m.value || 'empty'}</b>
                  <button aria-label="Remove this filter" onClick={() => remove(i)}>
                    <Close />
                  </button>
                </span>
              ))}
            </div>
          )}
          {failed ? (
            <Problem>Can't reach pierd. Trying again.</Problem>
          ) : !chart || !lines ? (
            <div className="panel">
              <Skeleton rows={5} />
            </div>
          ) : (
            <>
              <div className="panel le-chart">
                <div className="le-chart-head">
                  <span>Requests</span>
                  <div className="a-legend">
                    <span><span className="a-dot" style={{ background: 'var(--d-red)' }} />5xx<b>{totals.e5.toLocaleString()}</b></span>
                    <span><span className="a-dot" style={{ background: 'var(--d-amber)' }} />4xx<b>{totals.e4.toLocaleString()}</b></span>
                    <span><span className="a-dot" style={{ background: 'var(--d-blue)' }} />Others<b>{totals.rest.toLocaleString()}</b></span>
                  </div>
                </div>
                <Bars chart={chart} onPick={(b) => setZoom({ from: b.at, to: b.at + chart.bucket_ms })} />
              </div>
              <Lines lines={lines} open={open} setOpen={setOpen} onFilter={add} live={live} onTrace={onTrace} />
            </>
          )}
        </div>
      </div>
    </main>
  )
}

function FieldValues({ base, params, field, onPick }: { base: string; params: URLSearchParams; field: string; onPick: (v: string) => void }) {
  const [data, setData] = useState<{ values: { name: string; count: number }[]; rest: number } | null>(null)
  const key = `${field}?${params}`
  useEffect(() => {
    let stopped = false
    setData(null)
    api<{ values: { name: string; count: number }[]; rest: number }>(`${base}/values?field=${field}&${params}`).then((d) => !stopped && setData(d), () => {})
    return () => {
      stopped = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key])
  if (!data) return <div className="le-values le-note">Counting</div>
  if (!data.values.length) return <div className="le-values le-note">None in this range</div>
  const max = Math.max(...data.values.map((v) => v.count))
  return (
    <div className="le-values">
      {data.values.map((v) => (
        <button key={v.name} className="le-value" onClick={() => onPick(v.name)} title={`Only ${v.name || 'empty'}`}>
          <span className="le-value-row">
            <span className="mono">{v.name || 'empty'}</span>
            <span className="mono t-muted">{v.count.toLocaleString()}</span>
          </span>
          <span className="a-bar">
            <span style={{ width: `${(v.count / max) * 100}%` }} />
          </span>
        </button>
      ))}
      {data.rest > 0 && <div className="le-note">{data.rest.toLocaleString()} more</div>}
    </div>
  )
}

function Bars({ chart, onPick }: { chart: Chart; onPick: (b: Bucket) => void }) {
  const b = chart.buckets
  const max = Math.max(1, ...b.map((x) => x.requests))
  const span = b.length ? b[b.length - 1].at + chart.bucket_ms - b[0].at : 0
  const day = chart.bucket_ms >= 86_400_000
  const ticks = b.length ? [0, 0.25, 0.5, 0.75, 1].map((f) => b[0].at + f * span) : []
  return (
    <div className="le-bars-wrap">
      <div className="le-bars">
        {b.map((x) => {
          const rest = x.requests - x.e4xx - x.e5xx
          return (
            <button key={x.at} className="le-bar" onClick={() => x.requests && onPick(x)} title={`${clock(x.at, day)}: ${x.requests.toLocaleString()} requests, ${x.e5xx} 5xx, ${x.e4xx} 4xx`}>
              <span style={{ height: `${(x.e5xx / max) * 100}%`, background: 'var(--d-red)' }} />
              <span style={{ height: `${(x.e4xx / max) * 100}%`, background: 'var(--d-amber)' }} />
              <span style={{ height: `${(rest / max) * 100}%`, background: 'var(--d-blue)' }} />
            </button>
          )
        })}
      </div>
      <div className="a-x" style={{ gridColumn: 'auto' }}>
        {ticks.map((t, i) => (
          <span key={i}>{clock(t, day)}</span>
        ))}
      </div>
    </div>
  )
}

function Lines({ lines, open, setOpen, onFilter, live, onTrace }: {
  lines: Line[]
  open: number | null
  setOpen: (id: number | null) => void
  onFilter: (m: Match) => void
  live: boolean
  onTrace?: (id: string) => void
}) {
  const box = useRef<HTMLDivElement>(null)
  if (!lines.length) {
    return (
      <div className="panel a-empty">
        <div className="t-small">No requests match</div>
        <div className="t-caption t-muted">{live ? 'New ones show here as they come in.' : 'Try a wider range or fewer filters.'}</div>
      </div>
    )
  }
  return (
    <div className="table le-lines" ref={box} style={{ '--cols': '14px 112px 64px 56px 1fr 72px' } as React.CSSProperties}>
      <div className="t-head">
        <span />
        <span>Time</span>
        <span>Status</span>
        <span>Method</span>
        <span>Path</span>
        <span className="t-right">Time taken</span>
      </div>
      {lines.map((l) => {
        const kind = l.status >= 500 ? 'error' : l.status >= 400 ? 'warn' : ''
        const isOpen = open === l.id
        const record: [string, string, string?][] = [
          ['time', new Date(l.at).toISOString()],
          ['method', l.method, 'method'],
          ['path', l.path, 'path'],
          ['status', String(l.status), 'status'],
          ['time taken', `${l.ms.toFixed(1)} ms`],
          ['size', `${l.bytes.toLocaleString()} bytes`],
          ['host', l.host ?? '', 'host'],
          ['environment', l.env ?? '', 'env'],
          ['deployment', l.deployment, 'deployment'],
          ['browser', l.browser ?? '', 'browser'],
          ['device', l.device ?? '', 'device'],
          ['referrer', l.referrer ?? '', 'referrer'],
          ['country', l.country ?? '', 'country'],
          ['bot', l.bot ?? '', 'bot'],
          ['trace', l.trace ?? ''],
        ]
        return (
          <div key={l.id} className={'le-line ' + kind + (isOpen ? ' open' : '')}>
            <button className="t-row" onClick={() => setOpen(isOpen ? null : l.id)} aria-expanded={isOpen}>
              <span className={'le-chev' + (isOpen ? ' down' : '')}>
                <Disclosure />
              </span>
              <span className="mono">{clock(l.at, false, true)}</span>
              <span className="le-status">
                <span className="a-dot" style={{ background: kind === 'error' ? 'var(--d-red)' : kind === 'warn' ? 'var(--d-amber)' : 'var(--d-faint)' }} />
                <span className="mono">{l.status}</span>
              </span>
              <span className="mono t-muted">{l.method}</span>
              <span className="mono a-name">{l.path}</span>
              <span className="mono t-right t-muted">{l.ms < 1 ? '<1 ms' : `${Math.round(l.ms)} ms`}</span>
            </button>
            {isOpen && (
              <div className="le-record">
                <div className="le-record-head">
                  <span className="t-caption t-muted">Click a value to filter by it</span>
                  {l.trace && onTrace && (
                    <button className="btn quiet medium" onClick={() => onTrace(l.trace!)}>
                      Open its trace
                    </button>
                  )}
                  <button
                    className="btn quiet medium"
                    onClick={() => copy(JSON.stringify(Object.fromEntries(record.filter(([, v]) => v).map(([k, v]) => [k, v])), null, 2), 'the request')}
                  >
                    <Copy />
                    Copy
                  </button>
                </div>
                <dl>
                  {record
                    .filter(([, v]) => v !== '')
                    .map(([k, v, f]) => (
                      <div key={k}>
                        <dt>{k}</dt>
                        <dd>{f ? <button className="mono" onClick={() => onFilter({ field: f, value: f === 'path' ? v.split('?')[0] : v })}>{v}</button> : <span className="mono">{v}</span>}</dd>
                      </div>
                    ))}
                </dl>
              </div>
            )}
          </div>
        )
      })}
    </div>
  )
}

function clock(ms: number, day = false, seconds = false) {
  const d = new Date(ms)
  const t = d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: seconds ? '2-digit' : undefined, hour12: false })
  return day ? d.toLocaleDateString(undefined, { day: 'numeric', month: 'short' }) : seconds ? `${t}.${String(d.getMilliseconds()).padStart(3, '0')}` : t
}
