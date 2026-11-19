import { useEffect, useRef, useState } from 'react'
import { api } from '../lib/api'
import { Search } from '../components/Icons'
import { RequestsChart, type ChartData } from '../components/RequestsChart'

type Line = { id: number; deployment: string; at: number; method: string; path: string; status: number; ms: number; bytes: number }

const ranges = [
  { key: '15m', ms: 15 * 60_000 },
  { key: '1h', ms: 3_600_000 },
  { key: '24h', ms: 86_400_000 },
  { key: '7d', ms: 7 * 86_400_000 },
]

// Logs is H1: filters on top, since they drive both the chart and the list;
// the requests and errors charts; then the requests themselves, followed
// live while the range ends now.
export function Logs({ project, address }: { project: string; address: string }) {
  const [range, setRange] = useState('24h')
  const [status, setStatus] = useState('')
  const [query, setQuery] = useState('')
  const [q, setQ] = useState('') // the query, applied a moment after typing stops
  const [chart, setChart] = useState<ChartData | null>(null)
  const [lines, setLines] = useState<Line[]>([])
  const [loaded, setLoaded] = useState(false)
  const box = useRef<HTMLDivElement>(null)
  const atBottom = useRef(true)

  useEffect(() => {
    const t = window.setTimeout(() => setQ(query), 300)
    return () => window.clearTimeout(t)
  }, [query])

  useEffect(() => {
    let stopped = false
    let timer: number | undefined
    let after = 0
    let all: Line[] = []
    setLoaded(false)
    const span = ranges.find((r) => r.key === range)!.ms
    const base = `/api/projects/${encodeURIComponent(project)}/requests`
    const filters = () => {
      const to = Date.now()
      const p = new URLSearchParams({ from: String(to - span), to: String(to) })
      if (status) p.set('status', status)
      if (q) p.set('q', q)
      return p
    }
    const tick = async (first: boolean) => {
      try {
        const p = filters()
        const [c, l] = await Promise.all([
          api<ChartData>(`${base}/chart?${p}`),
          api<{ lines: Line[] }>(`${base}?${p}${after ? `&after=${after}` : ''}`),
        ])
        if (stopped) return
        setChart(c)
        all = first ? l.lines : [...all, ...l.lines].slice(-500)
        if (all.length) after = all[all.length - 1].id
        setLines(all)
        setLoaded(true)
      } catch {
        // keep what's shown; try again on the next tick
      }
      if (!stopped) timer = window.setTimeout(() => tick(false), 3000)
    }
    tick(true)
    return () => {
      stopped = true
      window.clearTimeout(timer)
    }
  }, [project, range, status, q])

  useEffect(() => {
    const el = box.current
    if (el && atBottom.current) el.scrollTop = el.scrollHeight
  }, [lines])

  const day = range === '7d'
  return (
    <main className="main" style={{ gap: 16 }}>
      <div className="toolbar">
        <label className="search-field" style={{ flex: 1, height: 44 }}>
          <Search size={18} />
          <input placeholder="Search paths" value={query} onChange={(e) => setQuery(e.target.value)} aria-label="Search paths" />
        </label>
        <select className="input" style={{ width: 170, fontFamily: 'var(--d-sans)' }} value={status} onChange={(e) => setStatus(e.target.value)} aria-label="Status">
          <option value="">All statuses</option>
          <option value="2xx">2xx</option>
          <option value="3xx">3xx</option>
          <option value="4xx">4xx</option>
          <option value="5xx">5xx</option>
          <option value="errors">Errors only</option>
        </select>
        <div className="range" role="radiogroup" aria-label="Time range">
          {ranges.map((r) => (
            <button key={r.key} className={range === r.key ? 'on' : ''} role="radio" aria-checked={range === r.key} onClick={() => setRange(r.key)}>
              {r.key}
            </button>
          ))}
        </div>
      </div>

      {chart && <RequestsChart data={chart} live />}

      <section className="panel log-viewer">
        <div className="panel-head" style={{ minHeight: 44, padding: '0 16px' }}>
          <span className="following">
            <i />
            Following
          </span>
          <span className="mono t-note">{project}</span>
          <span style={{ flex: 1 }} />
          <span className="t-note">Lines are kept for 14 days</span>
        </div>
        {loaded && lines.length === 0 ? (
          <div className="log-empty">
            No requests {status || q ? 'match' : 'in this range yet'}.{' '}
            {!status && !q && <>Open <span className="mono">{address}</span> and they show up here within a second.</>}
          </div>
        ) : (
          <div
            className="log requests-log"
            ref={box}
            onScroll={(e) => {
              const el = e.currentTarget
              atBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 24
            }}
          >
            {lines.map((l) => (
              <div key={l.id} className={'line' + (l.status >= 400 ? ' error' : '')}>
                <span className="when">{stamp(l.at, day)}</span>
                <span className="method">{l.method}</span>
                <span className={'code s' + String(l.status)[0]}>{l.status}</span>
                <span className="text">{l.path}</span>
                <span className="took">{l.ms < 10 ? l.ms.toFixed(1) : Math.round(l.ms)}ms</span>
              </div>
            ))}
          </div>
        )}
      </section>
    </main>
  )
}

function stamp(at: number, day: boolean): string {
  const d = new Date(at)
  const time = d.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit', second: '2-digit' })
  return day ? `${d.toLocaleDateString('en-GB', { day: 'numeric', month: 'short' })} ${time}` : time
}
