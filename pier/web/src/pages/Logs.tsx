import { useCallback, useEffect, useRef, useState } from 'react'
import { localDay, utcOffset } from '../lib/calendar'
import { api } from '../lib/api'
import { Calendar, Search } from '../components/Icons'
import { RequestsChart, type ChartData } from '../components/RequestsChart'
import { DateRangePicker, type Span } from '../components/DateRangePicker'
import { Problem, Skeleton } from '../components/ui'

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
  const params = new URLSearchParams(location.search)
  const [range, setRange] = useState(params.get('day') === 'yesterday' ? 'custom' : '24h') // a key from ranges, or 'custom'
  const [custom, setCustom] = useState<Span | null>(() => {
    const yesterday = localDay(new Date(), -1)
    return range === 'custom' ? { from: yesterday, to: yesterday } : null
  })
  const [picking, setPicking] = useState(false)
  const closePicker = useCallback(() => setPicking(false), [])
  const [status, setStatus] = useState(params.get('status') === 'errors' ? 'errors' : '')
  const [query, setQuery] = useState('')
  const [q, setQ] = useState('') // the query, applied a moment after typing stops
  const [chart, setChart] = useState<ChartData | null>(null)
  const [lines, setLines] = useState<Line[]>([])
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState(false)
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
    setError(false)
    setChart(null)
    setLines([])
    const base = `/api/projects/${encodeURIComponent(project)}/requests`
    // A custom range covers whole days; it follows live when it ends today.
    const bounds = () => {
      if (range === 'custom' && custom) {
        const end = localDay(custom.to, 1).getTime()
        return { from: custom.from.getTime(), to: Math.min(Date.now(), end), live: end > Date.now() }
      }
      const to = Date.now()
      return { from: to - ranges.find((r) => r.key === range)!.ms, to, live: true }
    }
    const filters = () => {
      const { from, to } = bounds()
      const p = new URLSearchParams({ from: String(from), to: String(to) })
      if (status) p.set('status', status)
      if (q) p.set('q', q)
      return p
    }
    const tick = async (first: boolean) => {
      try {
        const p = filters()
        // A chart of days counts the viewer's days. The offset is the one at
        // the start of the range, so the first day starts where the range does.
        const tz = utcOffset(Number(p.get('from')))
        const [c, l] = await Promise.all([
          api<ChartData>(`${base}/chart?${p}&tz=${tz}`),
          api<{ lines: Line[] }>(`${base}?${p}${after ? `&after=${after}` : ''}`),
        ])
        if (stopped) return
        setChart(c)
        all = first ? l.lines : [...all, ...l.lines].filter((line) => line.at >= Number(p.get('from'))).slice(-500)
        if (all.length) after = all[all.length - 1].id
        setLines(all)
        setLoaded(true)
        setError(false)
      } catch {
        if (!stopped) setError(true)
      }
      if (!stopped && bounds().live) timer = window.setTimeout(() => tick(false), 3000)
    }
    tick(true)
    return () => {
      stopped = true
      window.clearTimeout(timer)
    }
  }, [project, range, custom, status, q])
  const live = range !== 'custom' || !custom || localDay(custom.to, 1).getTime() > Date.now()
  const customLabel = custom
    ? custom.from.getTime() === custom.to.getTime()
      ? fmtDay(custom.from)
      : `${fmtDay(custom.from)} to ${fmtDay(custom.to)}`
    : 'Custom'

  useEffect(() => {
    const el = box.current
    if (el && atBottom.current) el.scrollTop = el.scrollHeight
  }, [lines])

  const day = range === '7d' || range === 'custom'
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
        <div style={{ position: 'relative' }}>
          <div className="range" role="radiogroup" aria-label="Time range">
            {ranges.map((r) => (
              <button
                key={r.key}
                className={range === r.key ? 'on' : ''}
                role="radio"
                aria-checked={range === r.key}
                onClick={() => {
                  setRange(r.key)
                  setPicking(false)
                }}
              >
                {r.key}
              </button>
            ))}
            <button
              className={'custom' + (range === 'custom' ? ' on' : '')}
              role="radio"
              aria-checked={range === 'custom'}
              aria-expanded={picking}
              onClick={() => setPicking(!picking)}
            >
              <Calendar />
              {range === 'custom' ? customLabel : 'Custom'}
            </button>
          </div>
          {picking && (
            <DateRangePicker
              value={custom}
              onCancel={closePicker}
              onApply={(s) => {
                setCustom(s === '24h' ? null : s)
                setRange(s === '24h' ? '24h' : 'custom')
                setPicking(false)
              }}
            />
          )}
        </div>
      </div>

      {error && <Problem>Couldn't load the request logs. {live ? 'Trying again in a moment.' : 'Reload the page to try again.'}</Problem>}
      {range === 'custom' && custom && (q || status) && custom.from.getTime() < Date.now() - 14 * 86_400_000 && (
        <span className="t-note">With a search or a status chosen, the charts count request lines, which go back 14 days.</span>
      )}
      {chart ? (
        <RequestsChart data={chart} live={live} />
      ) : (
        !error && (
          <div className="panel">
            <Skeleton rows={3} tall />
          </div>
        )
      )}

      <section className="panel log-viewer">
        <div className="panel-head" style={{ minHeight: 44, padding: '0 16px' }}>
          <span className={'following' + (live ? '' : ' paused')}>
            <i />
            {live ? 'Following' : 'Up to ' + (custom ? fmtDay(custom.to) : '')}
          </span>
          <span className="mono t-note">{project}</span>
          <span style={{ flex: 1 }} />
          <span className="t-note">
            {lines.length >= 500 ? 'The newest 500 lines of this range. ' : ''}Lines are kept for 14 days.
          </span>
        </div>
        {!loaded && !error ? (
          <Skeleton rows={5} />
        ) : loaded && lines.length === 0 ? (
          <div className="log-empty">
            No request lines {status || q ? 'match these filters' : 'in this range'}.{' '}
            {live && !status && !q && <>Open <span className="mono">{address}</span> and they show up here within a second.</>}
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

const fmtDay = (d: Date) => d.toLocaleDateString('en-GB', { day: 'numeric', month: 'short' })

function stamp(at: number, day: boolean): string {
  const d = new Date(at)
  const time = d.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit', second: '2-digit' })
  return day ? `${d.toLocaleDateString('en-GB', { day: 'numeric', month: 'short' })} ${time}` : time
}
