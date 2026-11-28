import { useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { api, type TraceDoc, type TraceList, type TraceRow, type TraceSpan } from '../lib/api'
import { useData } from '../lib/data'
import { size } from '../lib/format'
import { Gate, onlyOwner, useRole } from '../lib/role'
import { Calendar, Check, ChevronLeft, ChevronRight, Close, FailedMark, SortDown, Visit, Warning } from '../components/Icons'
import { explore, useGrafana } from '../lib/grafana'
import { Resizer, useWidth } from '../components/Resize'
import { Menu } from '../components/Menu'
import { toast } from '../components/Toast'
import { Problem, Skeleton } from '../components/ui'

// Traces: every request to a server app, from Pier's router through the
// app's own spans, kept in Tempo. The list plots each trace by when it
// started and how long it took; drag across the plot to pick some. A trace
// opens as a waterfall with the span's details beside it.

const ROUTER = 'pier'
const BLUE = '#3987e5'
const RED = 'var(--d-red)'
const palette = ['#d55181', '#9085e9', '#199e70', '#c98a2e', '#4fa3b8']
const assigned = new Map<string, string>()

// A service keeps its colour for the whole visit: Pier's router is blue,
// the rest take the next colour as they first appear.
export function serviceColor(service: string) {
  if (service === ROUTER) return BLUE
  let c = assigned.get(service)
  if (!c) {
    c = palette[assigned.size] ?? 'rgba(255, 255, 255, 0.4)'
    assigned.set(service, c)
  }
  return c
}

export function dur(v: number) {
  if (v < 1) return v <= 0 ? '0 ms' : '<1 ms'
  if (v < 1000) return `${Math.round(v)} ms`
  if (v < 60_000) return `${(v / 1000).toFixed(v < 10_000 ? 2 : 1)} s`
  return `${Math.floor(v / 60_000)} min ${Math.round((v % 60_000) / 1000)} s`
}

const pad = (n: number, w = 2) => String(n).padStart(w, '0')
function stamp(at: number, withDay = false) {
  const d = new Date(at)
  const t = `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}.${pad(d.getMilliseconds(), 3)}`
  return withDay ? `${d.toLocaleDateString(undefined, { day: 'numeric', month: 'short' })} ${t}` : t
}
function clock(at: number, withDay: boolean) {
  const d = new Date(at)
  const t = `${pad(d.getHours())}:${pad(d.getMinutes())}`
  return withDay ? `${d.toLocaleDateString(undefined, { day: 'numeric', month: 'short' })} ${t}` : t
}

// axis labels a power of ten: 0.1 ms, 10 ms, 1 s, 10 s.
function axis(v: number) {
  if (v < 1000) return `${+v.toPrecision(1)} ms`
  if (v < 60_000) return `${v / 1000} s`
  return `${v / 60_000} min`
}

const failed = (t: TraceRow) => (t.status ?? 0) >= 500 || t.errors > 0

function Status({ status, error }: { status?: number; error?: boolean }) {
  if ((status ?? 0) >= 500 || error) {
    return (
      <span className="tr-status">
        <span className="a-dot" style={{ background: RED }} />
        {status && status >= 500 ? `Error ${status}` : 'Error'}
      </span>
    )
  }
  return <span className="tr-status t-muted">{status && status >= 400 ? status : 'OK'}</span>
}

const ranges = [
  { key: '15m', label: 'Last 15 minutes', ms: 15 * 60_000 },
  { key: '1h', label: 'Last hour', ms: 3_600_000 },
  { key: '6h', label: 'Last 6 hours', ms: 6 * 3_600_000 },
  { key: '24h', label: 'Last 24 hours', ms: 86_400_000 },
  { key: '7d', label: 'Last 7 days', ms: 7 * 86_400_000 },
]

type Pick = { t0: number; t1: number; lo: number; hi: number }

export function Traces({ project, tabs, onOpen }: { project: string; tabs: (right?: ReactNode) => ReactNode; onOpen: (id: string) => void }) {
  const [range, setRange] = useState(ranges[1])
  const [draft, setDraft] = useState('')
  const [query, setQuery] = useState('')
  const [to, setTo] = useState(() => Date.now())
  const [pick, setPick] = useState<Pick | null>(null)
  useEffect(() => {
    const t = window.setInterval(() => setTo(Date.now()), 30_000)
    return () => window.clearInterval(t)
  }, [])
  const from = to - range.ms
  const second = (t: number) => t - (t % 1000)
  const q = new URLSearchParams({ from: String(second(from)), to: String(second(to) + 1000) })
  if (query) q.set('q', query)
  const list = useData<TraceList>(`/api/projects/${encodeURIComponent(project)}/traces?${q}`)
  const right = (
    <Menu label="Time range" align="end" className="btn quiet medium" items={ranges.map((r) => ({ label: r.label, onSelect: () => (setRange(r), setPick(null)) }))}>
      <Calendar />
      {range.label}
    </Menu>
  )
  const d = list.data
  if (d && !d.on) {
    return (
      <main className="main" style={{ gap: 28 }}>
        {tabs()}
        <TelemetryOff project={project} onOn={list.reload} />
      </main>
    )
  }
  const bad = list.error && list.error.status === 400 ? list.error.message : ''
  return (
    <main className="main" style={{ gap: 28 }}>
      {tabs(right)}
      <form
        className="tr-query"
        onSubmit={(e) => {
          e.preventDefault()
          setQuery(draft.trim())
          setPick(null)
        }}
      >
        <label className={'tr-input' + (bad ? ' bad' : '')}>
          <span className="t-caption t-note">TraceQL</span>
          <input
            className="mono"
            value={draft}
            spellCheck={false}
            placeholder="{ duration > 300ms && status = error }"
            onChange={(e) => setDraft(e.target.value)}
          />
        </label>
        {draft.trim() !== query && <button className="btn quiet medium">Run</button>}
        {query && draft.trim() === query && (
          <button type="button" className="btn quiet medium" onClick={() => (setDraft(''), setQuery(''), setPick(null))}>
            Clear
          </button>
        )}
      </form>
      {bad && <div className="tr-bad mono">{bad.replace(/^invalid TraceQL query: /, '')}</div>}
      {list.error && !bad ? (
        <Problem>{list.error.message}</Problem>
      ) : !d ? (
        <div className="panel">
          <Skeleton rows={4} tall />
        </div>
      ) : d.traces.length === 0 ? (
        <div className="panel a-empty">
          <div className="t-small">{query ? 'No traces match' : 'No traces in this range'}</div>
          <div className="t-caption t-muted">{query ? 'Try a wider range, or a looser query.' : 'Requests to server apps show here a few seconds after they finish.'}</div>
        </div>
      ) : (
        <>
          <ServiceMap
            project={project}
            from={second(from)}
            to={second(to) + 1000}
            onPick={(q) => {
              setDraft(q)
              setQuery(q)
              setPick(null)
            }}
          />
          <TraceLists d={d} from={from} to={to} pick={pick} setPick={setPick} onOpen={onOpen} />
        </>
      )}
    </main>
  )
}

function TraceLists({ d, from, to, pick, setPick, onOpen }: { d: TraceList; from: number; to: number; pick: Pick | null; setPick: (p: Pick | null) => void; onOpen: (id: string) => void }) {
  const [shown, setShown] = useState(8)
  const [by, setBy] = useState<'ms' | 'start'>('ms')
  useEffect(() => setShown(8), [pick, d])
  // Colours in the order services first appear, oldest trace first.
  const services = useMemo(() => {
    const seen: string[] = []
    for (const t of [...d.traces].sort((a, b) => a.start - b.start)) {
      for (const s of [t.service, ...Object.keys(t.services).sort()]) if (s && !s.startsWith('<') && !seen.includes(s)) seen.push(s)
    }
    seen.forEach(serviceColor)
    return seen.sort((a, b) => (a === ROUTER ? -1 : b === ROUTER ? 1 : 0))
  }, [d])
  const inPick = pick ? d.traces.filter((t) => t.start >= pick.t0 && t.start <= pick.t1 && t.ms >= pick.lo && t.ms <= pick.hi) : d.traces
  const rows = [...inPick].sort((a, b) => (by === 'ms' ? b.ms - a.ms : b.start - a.start))
  const errors = d.traces.filter(failed).length
  const longest = Math.max(1, ...rows.map((t) => t.ms))
  const withDay = to - from > 26 * 3600_000
  return (
    <>
      <section className="a-section">
        <div className="a-section-head">
          <h2>Traces</h2>
          <div className="a-legend">
            <span>
              <span className="a-dot" style={{ background: BLUE }} />
              OK<b>{(d.traces.length - errors).toLocaleString()}</b>
            </span>
            <span>
              <span className="a-dot" style={{ background: RED }} />
              Error<b>{errors.toLocaleString()}</b>
            </span>
          </div>
        </div>
        <div className="panel tr-scatter-panel">
          <Scatter traces={d.traces} from={from} to={to} pick={pick} setPick={setPick} onOpen={onOpen} count={inPick.length} />
        </div>
      </section>
      <section className="a-section">
        <div className="a-section-head">
          <h2>
            {pick ? 'In the selection' : 'Slowest first'}
            {pick && (
              <span>
                {clock(pick.t0, withDay)} to {clock(pick.t1, withDay)}, {dur(pick.lo)} to {dur(pick.hi)}
              </span>
            )}
          </h2>
          <div className="a-legend">
            {services.map((s) => (
              <span key={s}>
                <span className="a-dot" style={{ background: serviceColor(s) }} />
                {s}
              </span>
            ))}
          </div>
        </div>
        <div className="table tr-table" style={{ '--cols': '124px minmax(0, 1.3fr) 92px 48px minmax(0, 1.3fr) 96px' } as React.CSSProperties}>
          <div className="t-head">
            <button className={'sort' + (by === 'start' ? ' on' : '')} onClick={() => setBy('start')}>
              Started
              {by === 'start' && (
                <span className="sort-arrow">
                  <SortDown color="currentColor" />
                </span>
              )}
            </button>
            <span>Root span</span>
            <span>Services</span>
            <span className="t-right">Spans</span>
            <button className={'sort' + (by === 'ms' ? ' on' : '')} onClick={() => setBy('ms')}>
              Duration
              {by === 'ms' && (
                <span className="sort-arrow">
                  <SortDown color="currentColor" />
                </span>
              )}
            </button>
            <span>Status</span>
          </div>
          {rows.length === 0 && <div className="a-none">Nothing in the selection</div>}
          {rows.slice(0, shown).map((t) => (
            <button key={t.id} className="t-row tr-row" onClick={() => onOpen(t.id)}>
              <span className="mono">{stamp(t.start, withDay)}</span>
              {t.partial ? <span className="t-muted">Still arriving</span> : <span className="mono">{t.name || 'No name'}</span>}
              <span className="tr-dots">
                {Object.keys(t.services)
                  .filter((s) => !s.startsWith('<'))
                  .sort((a, b) => services.indexOf(a) - services.indexOf(b))
                  .map((s) => (
                    <span key={s} className="a-dot" style={{ background: serviceColor(s) }} title={s} />
                  ))}
              </span>
              <span className="t-right mono t-muted">{t.spans}</span>
              <span className="tr-dur">
                <span className="a-bar">
                  <span style={{ width: `${(t.ms / longest) * 100}%` }} />
                </span>
                <span className="mono">{dur(t.ms)}</span>
              </span>
              <Status status={t.status} error={t.errors > 0} />
            </button>
          ))}
          {rows.length > shown && (
            <button className="tr-more" onClick={() => setShown((n) => n + 25)}>
              Show {Math.min(25, rows.length - shown)} more
            </button>
          )}
          {d.traces.length >= d.limit && rows.length <= shown && (
            <div className="a-foot">The newest {d.limit.toLocaleString()} traces. A shorter range or a query finds older ones.</div>
          )}
        </div>
      </section>
    </>
  )
}

// Scatter plots each trace by when it began and how long it took, on a log
// scale, errors in red. Dragging picks a box; clicking a dot opens it.
function Scatter({ traces, from, to, pick, setPick, onOpen, count }: {
  traces: TraceRow[]
  from: number
  to: number
  pick: Pick | null
  setPick: (p: Pick | null) => void
  onOpen: (id: string) => void
  count: number
}) {
  const box = useRef<HTMLDivElement>(null)
  const [w, setW] = useState(900)
  const [drag, setDrag] = useState<{ x0: number; y0: number; x1: number; y1: number } | null>(null)
  const [hover, setHover] = useState<TraceRow | null>(null)
  useLayoutEffect(() => {
    const el = box.current
    if (!el) return
    const ro = new ResizeObserver(() => setW(el.clientWidth))
    ro.observe(el)
    setW(el.clientWidth)
    return () => ro.disconnect()
  }, [])
  const H = 200
  const vals = traces.map((t) => Math.max(t.ms, 0.1))
  const lo = 10 ** Math.floor(Math.log10(Math.min(...vals)))
  const hi = 10 ** Math.max(Math.ceil(Math.log10(Math.max(...vals))), Math.log10(lo) + 2)
  const L0 = Math.log10(lo)
  const L1 = Math.log10(hi)
  const x = (at: number) => ((at - from) / (to - from)) * w
  const y = (v: number) => H - ((Math.log10(Math.max(v, 0.1)) - L0) / (L1 - L0)) * H
  const atX = (px: number) => from + (px / w) * (to - from)
  const atY = (py: number) => 10 ** (L0 + ((H - py) / H) * (L1 - L0))
  const powers: number[] = []
  for (let p = L0; p <= L1 + 1e-9; p++) powers.push(10 ** p)
  const withDay = to - from > 26 * 3600_000
  const local = (e: React.MouseEvent) => {
    const r = box.current!.getBoundingClientRect()
    return { px: Math.max(0, Math.min(w, e.clientX - r.left)), py: Math.max(0, Math.min(H, e.clientY - r.top)) }
  }
  const nearest = (px: number, py: number) => {
    let best: TraceRow | null = null
    let bd = 64
    for (const t of traces) {
      const dx = x(t.start) - px
      const dy = y(t.ms) - py
      const dd = dx * dx + dy * dy
      if (dd < bd) (bd = dd), (best = t)
    }
    return best
  }
  const down = (e: React.MouseEvent) => {
    const { px, py } = local(e)
    setDrag({ x0: px, y0: py, x1: px, y1: py })
  }
  const move = (e: React.MouseEvent) => {
    const { px, py } = local(e)
    if (drag) setDrag({ ...drag, x1: px, y1: py })
    else setHover(nearest(px, py))
  }
  const up = () => {
    if (!drag) return
    const small = Math.abs(drag.x1 - drag.x0) < 4 && Math.abs(drag.y1 - drag.y0) < 4
    if (small) {
      const t = nearest(drag.x0, drag.y0)
      if (t) onOpen(t.id)
      else setPick(null)
    } else {
      const [xa, xb] = [Math.min(drag.x0, drag.x1), Math.max(drag.x0, drag.x1)]
      const [ya, yb] = [Math.min(drag.y0, drag.y1), Math.max(drag.y0, drag.y1)]
      setPick({ t0: atX(xa), t1: atX(xb), lo: atY(yb), hi: atY(ya) })
    }
    setDrag(null)
  }
  const rect = drag
    ? { left: Math.min(drag.x0, drag.x1), top: Math.min(drag.y0, drag.y1), width: Math.abs(drag.x1 - drag.x0), height: Math.abs(drag.y1 - drag.y0) }
    : pick
      ? { left: x(pick.t0), top: y(pick.hi), width: x(pick.t1) - x(pick.t0), height: y(pick.lo) - y(pick.hi) }
      : null
  const ok = traces.filter((t) => !failed(t))
  const bad = traces.filter(failed)
  return (
    <div className="tr-scatter">
      <div className="tr-y">
        {powers.map((p) => (
          <span key={p} style={{ top: y(p) }}>
            {axis(p)}
          </span>
        ))}
      </div>
      <div
        className="tr-plot"
        ref={box}
        style={{ height: H }}
        onMouseDown={down}
        onMouseMove={move}
        onMouseUp={up}
        onMouseLeave={() => (setHover(null), drag && up())}
      >
        <svg width={w} height={H} aria-hidden>
          {powers.map((p) => (
            <line key={p} x1="0" x2={w} y1={y(p)} y2={y(p)} className="a-grid" />
          ))}
          <line x1="0" x2={w} y1={H} y2={H} className="a-base" />
          {ok.map((t) => (
            <circle key={t.id} cx={x(t.start)} cy={y(t.ms)} r="3" fill={BLUE} fillOpacity="0.75" />
          ))}
          {bad.map((t) => (
            <circle key={t.id} cx={x(t.start)} cy={y(t.ms)} r="3.5" fill="#f07a7a" />
          ))}
          {hover && !drag && <circle cx={x(hover.start)} cy={y(hover.ms)} r="6" fill="none" stroke="var(--d-text)" strokeWidth="1.5" />}
        </svg>
        {rect && rect.width > 0 && (
          <div className="tr-pick" style={rect}>
            {!drag && <span>{count.toLocaleString()} selected</span>}
          </div>
        )}
        {hover && !drag && (
          <div className={'a-tip' + (x(hover.start) > w * 0.7 ? ' left' : '')} style={{ left: x(hover.start), top: Math.max(0, y(hover.ms) - 20) }}>
            <div className="a-tip-head">{stamp(hover.start, withDay)}</div>
            <div className="mono">{hover.name}</div>
            <div>
              <i style={{ background: failed(hover) ? RED : BLUE }} />
              {failed(hover) ? (hover.status ? `Error ${hover.status}` : 'Error') : 'OK'}
              <b>{dur(hover.ms)}</b>
            </div>
          </div>
        )}
      </div>
      <div className="a-x">
        {[0, 0.25, 0.5, 0.75, 1].map((f) => (
          <span key={f}>{clock(from + f * (to - from), withDay)}</span>
        ))}
      </div>
    </div>
  )
}

// Node is a span in the waterfall's tree.
type Node = TraceSpan & { depth: number; children: Node[]; self: number }

function tree(spans: TraceSpan[]) {
  const byId = new Map<string, Node>()
  for (const s of spans) byId.set(s.id, { ...s, depth: 0, children: [], self: s.ms })
  const roots: Node[] = []
  for (const n of byId.values()) {
    const p = n.parent ? byId.get(n.parent) : undefined
    if (p) p.children.push(n)
    else roots.push(n)
  }
  const walk = (n: Node, depth: number) => {
    n.depth = depth
    n.children.sort((a, b) => a.start - b.start)
    // Self time: the span's own time, less what its children covered.
    const iv = n.children.map((c) => [Math.max(c.start, n.start), Math.min(c.start + c.ms, n.start + n.ms)]).filter(([a, b]) => b > a).sort((a, b) => a[0] - b[0])
    let covered = 0
    let end = -Infinity
    for (const [a, b] of iv) {
      if (b <= end) continue
      covered += b - Math.max(a, end)
      end = b
    }
    n.self = Math.max(0, n.ms - covered)
    n.children.forEach((c) => walk(c, depth + 1))
  }
  roots.sort((a, b) => a.start - b.start)
  roots.forEach((r) => walk(r, 0))
  return { roots, byId }
}

export function TraceView({ project, id, onBack }: { project: string; id: string; onBack: () => void }) {
  const t = useData<TraceDoc>(`/api/projects/${encodeURIComponent(project)}/traces/${id}`)
  const g = useGrafana(project)
  const head = (
    <button className="tr-crumb" onClick={onBack}>
      <ChevronLeft />
      <span>Traces</span>
      <span>/</span>
      <span className="mono">{id}</span>
    </button>
  )
  if (t.error) {
    return (
      <>
        {head}
        <Problem>{t.error.message}</Problem>
      </>
    )
  }
  if (!t.data) {
    return (
      <>
        {head}
        <div className="panel">
          <Skeleton rows={6} tall />
        </div>
      </>
    )
  }
  return <Waterfall doc={t.data} head={head} grafana={g} />
}

function Waterfall({ doc, head, grafana }: { doc: TraceDoc; head: ReactNode; grafana: ReturnType<typeof useGrafana> }) {
  const { trace, request, logs } = doc
  const { roots, byId } = useMemo(() => tree(trace.spans), [trace])
  const [closed, setClosed] = useState<Set<string>>(new Set())
  const all = useMemo(() => {
    const out: Node[] = []
    const add = (n: Node) => (out.push(n), n.children.forEach(add))
    roots.forEach(add)
    return out
  }, [roots])
  // Open where the failure started, the deepest span in error; or else
  // on the span that spent the most time itself, where the time went.
  const [sel, setSel] = useState<string | null>(() => {
    const origin = all.filter((s) => s.error && !s.children.some((c) => c.error)).sort((a, b) => b.depth - a.depth || a.start - b.start)[0]
    if (origin) return origin.id
    return all.length > 1 ? [...all].sort((a, b) => b.self - a.self)[0].id : null
  })
  const [logSpan, setLogSpan] = useState<string | null>(null)
  const detailW = useWidth('pier.trace.details', 340, 260, 720)
  // Pier's router only passes a request on; a gap before the app's own span
  // is the app not taking it yet: busy, starting or short of CPU.
  const firstApp = roots[0]?.service === ROUTER ? roots[0].children.find((c) => c.service !== ROUTER) : undefined
  const wait = firstApp ? firstApp.start - roots[0].start : 0
  const waited = wait >= 200 && wait >= trace.ms * 0.05
  const around = useData<{ samples: { at: number; cpu: number }[] }>(waited ? `/api/server/history?at=${Math.round(trace.start)}` : null)
  const cpu = Math.max(-1, ...(around.data?.samples ?? []).filter((x) => Math.abs(x.at - trace.start) <= 90_000).map((x) => x.cpu))
  const labelW = useWidth('pier.trace.names', 380, 200, 900)
  const logsRef = useRef<HTMLElement>(null)
  const rows = useMemo(() => {
    const out: Node[] = []
    const add = (n: Node) => {
      out.push(n)
      if (!closed.has(n.id)) n.children.forEach(add)
    }
    roots.forEach(add)
    return out
  }, [roots, closed])
  const T = Math.max(trace.ms, 0.001)
  const root = roots[0]
  const services = [...new Set(all.map((s) => s.service))]
  services.forEach(serviceColor)
  const deployment = request?.deployment || root?.resource?.find(([k]) => k === 'pier.deployment')?.[1]
  const error = (request?.status ?? 0) >= 500 || all.some((s) => s.error)
  const selected = sel ? byId.get(sel) : undefined
  const bySvc = services.map((s) => [s, all.filter((n) => n.service === s).reduce((a, n) => a + n.self, 0)] as const).sort((a, b) => b[1] - a[1])
  const selfTotal = Math.max(0.001, bySvc.reduce((a, [, v]) => a + v, 0))
  const pitch = Math.min(2.6, 30 / Math.max(1, all.length))
  const showLogs = (span: string | null) => {
    setLogSpan(span)
    requestAnimationFrame(() => logsRef.current?.scrollIntoView({ behavior: 'smooth', block: 'start' }))
  }
  return (
    <>
      <div className="tr-title">
        <div className="tr-title-main">
          {head}
          <div className="tr-name">
            <h1>{root?.name ?? trace.id}</h1>
            <Status status={request?.status} error={error} />
          </div>
          <div className="tr-meta">
            <span>
              Duration <b className="mono">{dur(trace.ms)}</b>
            </span>
            <span>
              Spans <b className="mono">{all.length}</b>
            </span>
            <span>
              Services <b className="mono">{services.length}</b>
            </span>
            <span>
              Started <b className="mono">{stamp(trace.start, Date.now() - trace.start > 20 * 3600_000)}</b>
            </span>
            {deployment && (
              <span>
                Deployment <b className="mono">{deployment.slice(0, 6)}</b>
              </span>
            )}
          </div>
        </div>
        <div className="tr-title-actions">
          {logs.length > 0 && (
            <button className="btn quiet medium" onClick={() => showLogs(null)}>
              Logs, {logs.length}
            </button>
          )}
          {grafana && (
            <a className="btn quiet medium" href={explore(grafana, 'traces', { queryType: 'traceql', query: trace.id }, trace.start - 60_000, trace.start + trace.ms + 60_000)} target="_blank" rel="noreferrer">
              Open in Grafana
              <Visit size={13} />
            </a>
          )}
        </div>
      </div>
      {waited && firstApp && (
        <div className="tr-wait">
          <span className="a-dot" style={{ background: 'var(--d-amber)' }} />
          <span>
            {firstApp.service} took the request {dur(wait)} after Pier's router passed it on, so the app was busy or starting.
            {cpu >= 0 && ` The server's CPU was at ${Math.round(cpu)}% then.`}
          </span>
        </div>
      )}
      <div className="panel tr-view">
        <div className="tr-mini">
          <svg viewBox={`0 0 1000 ${Math.max(8, all.length * pitch + 6)}`} preserveAspectRatio="none" aria-hidden>
            {all.map((s, i) => (
              <rect key={s.id} x={(s.start / T) * 1000} y={3 + i * pitch} width={Math.max(3, (s.ms / T) * 1000)} height={pitch * 0.62} rx="0.8" fill={serviceColor(s.service)} />
            ))}
          </svg>
        </div>
        <div className={'tr-body' + (selected ? ' with-detail' : '')} style={{ '--tr-detail': `${detailW.width}px` } as React.CSSProperties}>
          {selected && <Resizer w={detailW} panel="right" label="Resize the span's details" style={{ right: detailW.width - 5 }} />}
          <div className="tr-water" style={{ '--tr-label': `${labelW.width}px` } as React.CSSProperties}>
            <Resizer w={labelW} panel="left" label="Resize the span names" style={{ left: `min(${labelW.width}px, 75%)`, marginLeft: -5 }} />
            <div className="tr-row tr-axis">
              <span className="tr-label t-note">Span</span>
              <span className="tr-track">
                {[0, 0.25, 0.5, 0.75, 1].map((f) => (
                  <span key={f} className="tr-tick" style={{ left: `${f * 100}%` }}>
                    {f === 0 ? '0' : dur(f * T)}
                  </span>
                ))}
              </span>
            </div>
            <div className="tr-rows">
              <div className="tr-grid">
                {[0, 0.25, 0.5, 0.75, 1].map((f) => (
                  <span key={f} style={{ left: `${f * 100}%` }} />
                ))}
              </div>
              {rows.map((s) => (
                <div key={s.id} className={'tr-row tr-span' + (s.id === sel ? ' on' : '')} onClick={() => setSel(s.id === sel ? null : s.id)}>
                  <span className="tr-label" style={{ paddingLeft: 14 + s.depth * 14 }}>
                    {s.children.length > 0 ? (
                      <button
                        className={'tr-chev' + (closed.has(s.id) ? ' shut' : '')}
                        aria-label={closed.has(s.id) ? 'Show its spans' : 'Hide its spans'}
                        onClick={(e) => {
                          e.stopPropagation()
                          const next = new Set(closed)
                          if (next.has(s.id)) next.delete(s.id)
                          else next.add(s.id)
                          setClosed(next)
                        }}
                      >
                        <SortDown color="currentColor" />
                      </button>
                    ) : (
                      <span className="tr-chev-gap" />
                    )}
                    <span className="a-dot" style={{ background: serviceColor(s.service) }} />
                    <span className="tr-svc mono">{s.service}</span>
                    <span className="tr-span-name">{s.name}</span>
                    {s.error && <span className="a-dot tr-err" style={{ background: RED }} />}
                    <span className="tr-ms mono">{dur(s.ms)}</span>
                  </span>
                  <span className="tr-track">
                    <span className="tr-bar" style={{ left: `${(s.start / T) * 100}%`, width: `${(s.ms / T) * 100}%`, background: serviceColor(s.service) }} />
                  </span>
                </div>
              ))}
            </div>
            <div className="tr-self">
              <div className="tr-self-head">
                <span>Time by service</span>
                <span className="t-note">Working, not waiting on others</span>
              </div>
              <div className="tr-self-bar">
                {bySvc
                  .filter(([, v]) => v > 0)
                  .map(([s, v]) => (
                    <span key={s} style={{ width: `${(v / selfTotal) * 100}%`, background: serviceColor(s) }} />
                  ))}
              </div>
              <div className="a-legend">
                {bySvc.map(([s, v]) => (
                  <span key={s}>
                    <span className="a-dot" style={{ background: serviceColor(s) }} />
                    {s === ROUTER && waited ? 'pier, waiting for the app' : s}
                    <b>{dur(v)}</b>
                  </span>
                ))}
              </div>
            </div>
          </div>
          {selected && (
            <SpanDetail
              s={selected}
              parent={selected.parent ? byId.get(selected.parent) : undefined}
              total={T}
              logs={logs.filter((l) => l.span === selected.id).length}
              onLogs={() => showLogs(selected.id)}
              onClose={() => setSel(null)}
            />
          )}
        </div>
      </div>
      {logs.length > 0 && (
        <section className="a-section" ref={logsRef}>
          <div className="a-section-head">
            <h2>
              Logs
              {logSpan && <span>of {byId.get(logSpan)?.name ?? 'the span'}</span>}
            </h2>
            {logSpan && (
              <button className="btn quiet small" onClick={() => setLogSpan(null)}>
                All of the trace's
              </button>
            )}
          </div>
          <div className="panel tr-logs">
            {logs
              .filter((l) => !logSpan || l.span === logSpan)
              .map((l, i) => (
                <div key={i} className="tr-log">
                  <span className="mono t-note">+{dur(Math.max(0, l.at - trace.start))}</span>
                  <span className="tr-level">
                    <span className="a-dot" style={{ background: level(l.level) }} />
                    {l.level || 'log'}
                  </span>
                  <span className="mono t-muted">{l.service}</span>
                  <span className="mono tr-log-text">{l.text}</span>
                </div>
              ))}
          </div>
        </section>
      )}
    </>
  )
}

function level(l?: string) {
  if (!l) return 'rgba(255, 255, 255, 0.25)'
  if (/^(error|fatal|critical|crit|emerg|alert|panic)/i.test(l)) return RED
  if (/^warn/i.test(l)) return 'var(--d-amber)'
  return 'rgba(255, 255, 255, 0.25)'
}

function SpanDetail({ s, parent, total, logs, onLogs, onClose }: { s: Node; parent?: Node; total: number; logs: number; onLogs: () => void; onClose: () => void }) {
  const resource = (s.resource ?? []).filter(([k]) => k !== 'service.name' && k !== 'pier.project')
  const http = s.attrs?.find(([k]) => k === 'http.response.status_code' || k === 'http.status_code')?.[1]
  return (
    <aside className="tr-detail">
      <div className="tr-detail-in">
      <div className="tr-detail-head">
        <div>
          <div className="tr-detail-name">{s.name}</div>
          <div className="tr-detail-svc">
            <span className="a-dot" style={{ background: serviceColor(s.service) }} />
            <span>
              <span className="mono">{s.service}</span>
              {parent && (
                <>
                  , called by <span className="mono">{parent.name}</span>
                </>
              )}
            </span>
          </div>
        </div>
        <button className="icon-btn" aria-label="Close" onClick={onClose}>
          <Close />
        </button>
      </div>
      <div className="tr-facts">
        <span>Duration</span>
        <span>
          <span className="mono">{dur(s.ms)}</span>
          <span className="t-muted">, {Math.round((s.ms / total) * 100)}% of the trace</span>
        </span>
        <span>Started</span>
        <span>
          <span className="mono">{dur(s.start)}</span>
          <span className="t-muted"> in</span>
        </span>
        <span>Status</span>
        <span>
          {s.error ? (
            <span className="tr-status">
              <span className="a-dot" style={{ background: RED }} />
              {s.message || 'Error'}
            </span>
          ) : http ? (
            `OK, ${http}`
          ) : (
            'OK'
          )}
        </span>
        {s.kind && (
          <>
            <span>Kind</span>
            <span>{s.kind[0].toUpperCase() + s.kind.slice(1)}</span>
          </>
        )}
        <span>Span</span>
        <span className="mono">{s.id}</span>
      </div>
      {(s.attrs?.length ?? 0) > 0 && <Attrs title="Attributes" list={s.attrs!} />}
      {(s.events ?? []).map((e, i) => (
        <Attrs key={i} title={e.name === 'exception' ? 'Exception' : e.name} note={`${dur(e.at)} in`} list={e.attrs ?? []} />
      ))}
      {resource.length > 0 && <Attrs title="Service" list={resource} folded />}
      {logs > 0 && (
        <div className="tr-goto">
          <button onClick={onLogs}>
            <span>Logs for this span</span>
            <span className="mono t-muted">{logs}</span>
            <ChevronRight />
          </button>
        </div>
      )}
      </div>
    </aside>
  )
}

function Attrs({ title, note, list, folded }: { title: string; note?: string; list: [string, string][]; folded?: boolean }) {
  const [open, setOpen] = useState(!folded)
  return (
    <div className="tr-attrs">
      {folded ? (
        <button className="tr-attrs-head tr-fold" aria-expanded={open} onClick={() => setOpen(!open)}>
          {title}
          <span className="t-note">
            {list.length} attributes
            <span className={'tr-fold-chev' + (open ? ' open' : '')}>
              <SortDown color="currentColor" />
            </span>
          </span>
        </button>
      ) : (
        <div className="tr-attrs-head">
          {title}
          {note && <span className="t-note">{note}</span>}
        </div>
      )}
      {open && (
        <div className="tr-attr-grid">
          {list.map(([k, v], i) =>
            k === 'exception.stacktrace' ? (
              <div key={k + i} className="tr-attr-wide">
                <span className="t-muted">{k}</span>
                <pre>{v}</pre>
              </div>
            ) : (
              <div key={k + i} className="tr-attr">
                <span className="t-muted" title={k}>
                  {k}
                </span>
                <span>{v}</span>
              </div>
            ),
          )}
        </div>
      )}
    </div>
  )
}

type Telemetry = {
  available: boolean
  doing?: '' | 'starting' | 'stopping'
  status?: { on: boolean; retention_days: number }
}

const snippets: Record<string, [string, string][]> = {
  Node: [
    ['# once', ''],
    ['npm install @opentelemetry/auto-instrumentations-node', 'cmd'],
    ['# start command', ''],
    ['node --require @opentelemetry/auto-instrumentations-node/register server.js', 'cmd'],
  ],
  Python: [
    ['# once', ''],
    ['pip install opentelemetry-distro opentelemetry-exporter-otlp', 'cmd'],
    ['opentelemetry-bootstrap -a install', 'cmd'],
    ['# start command', ''],
    ['opentelemetry-instrument python app.py', 'cmd'],
  ],
  Go: [
    ['// the exporter reads the OTEL_ variables', ''],
    ['exp, _ := otlptracehttp.New(ctx)', 'cmd'],
    ['otel.SetTracerProvider(trace.NewTracerProvider(trace.WithBatcher(exp)))', 'cmd'],
    ['otel.SetTextMapPropagator(propagation.TraceContext{})', 'cmd'],
    ['http.ListenAndServe(addr, otelhttp.NewHandler(mux, "server"))', 'cmd'],
  ],
}

// TelemetryOff is the Traces tab before the server keeps traces: what
// turning them on runs, and what apps then get.
export function TelemetryOff({ project, onOn }: { project: string; onOn: () => void }) {
  const { owner } = useRole()
  const t = useData<Telemetry>('/api/server/telemetry', (d) => !!d.doing, 2000)
  const server = useData<{ memory_total?: number; memory_used?: number }>('/api/server')
  const [lang, setLang] = useState('Node')
  const doing = t.data?.doing
  useEffect(() => {
    if (t.data?.status?.on && !doing) onOn()
  }, [t.data?.status?.on, doing, onOn])
  const turnOn = async () => {
    try {
      await api('/api/server/telemetry', { method: 'PUT', body: JSON.stringify({ on: true }) })
      t.reload()
    } catch (e) {
      toast(e instanceof Error ? e.message : "Couldn't turn them on", { tone: 'failed' })
    }
  }
  const total = server.data?.memory_total ?? 0
  const used = server.data?.memory_used ?? 0
  const stack = 1536 * 2 ** 20 // the four servers' memory limits
  const runs: [string, string][] = [
    ['Tempo', 'traces'],
    ['Loki', 'app logs'],
    ['Prometheus', 'metrics'],
    ['Grafana', 'Grafana’s own views'],
  ]
  const vars: [string, string][] = [
    ['OTEL_EXPORTER_OTLP_ENDPOINT', `${location.origin}/otlp`],
    ['OTEL_EXPORTER_OTLP_HEADERS', 'authorization=Bearer ••••'],
    ['OTEL_SERVICE_NAME', project],
    ['OTEL_RESOURCE_ATTRIBUTES', 'deployment.environment=production'],
  ]
  return (
    <>
      <div className="tr-off-head">
        <div className="t-strong">No traces yet</div>
        <div className="t-caption t-muted">Every request to a server app gets a trace from Pier's router once this is on.</div>
      </div>
      <div className="tr-off">
        <section className="a-section">
          <div className="a-section-head">
            <h2>Turn on traces, metrics and app logs</h2>
          </div>
          <div className="panel tr-off-panel">
            <div className="tr-kv tr-kv-head">
              <span>Runs</span>
              <span>Keeps</span>
            </div>
            {runs.map(([k, v]) => (
              <div key={k} className="tr-kv">
                <span className="t-strong">{k}</span>
                <span className="t-muted">{v}</span>
              </div>
            ))}
            {total > 0 && (
              <div className="tr-mem">
                <div className="tr-mem-head">
                  <span>Memory</span>
                  <span className="t-muted">
                    <span className="mono">{size(stack)}</span> of <span className="mono">{size(Math.max(0, total - used))}</span> free
                  </span>
                </div>
                <div className="tr-mem-bar">
                  <span style={{ width: `${(used / total) * 100}%`, background: 'rgba(255, 255, 255, 0.22)' }} />
                  <span style={{ width: `${(Math.min(stack, total - used) / total) * 100}%`, background: BLUE }} />
                </div>
                <div className="a-legend">
                  <span>
                    <span className="a-dot" style={{ background: 'rgba(255, 255, 255, 0.35)' }} />
                    In use<b>{size(used)}</b>
                  </span>
                  <span>
                    <span className="a-dot" style={{ background: BLUE }} />
                    These<b>{size(stack)}</b>
                  </span>
                  <span>
                    <span className="a-dot" style={{ background: 'rgba(255, 255, 255, 0.12)' }} />
                    Free after<b>{size(Math.max(0, total - used - stack))}</b>
                  </span>
                </div>
              </div>
            )}
            <div className="tr-off-foot">
              <Gate allow={owner} reason={onlyOwner('turn these on')}>
                <button className="btn primary medium" disabled={!!doing || !t.data?.available} onClick={turnOn}>
                  {doing === 'starting' ? 'Starting' : 'Turn on'}
                </button>
              </Gate>
              <span className="t-caption t-muted">
                {doing === 'starting'
                  ? 'Downloading and starting the servers. This takes a minute the first time.'
                  : `Kept ${t.data?.status?.retention_days ?? 14} days. Overview and Events work without it.`}
              </span>
            </div>
          </div>
        </section>
        <section className="a-section">
          <div className="a-section-head">
            <h2>Then send data</h2>
          </div>
          <div className="panel tr-off-panel">
            <div className="tr-kv tr-kv-head">
              <span>Every deploy gets</span>
            </div>
            {vars.map(([k, v]) => (
              <div key={k} className="tr-kv tr-var">
                <span className="mono">{k}</span>
                <span className="mono t-muted">{v}</span>
              </div>
            ))}
            <div className="tr-code-head">
              <span className="t-strong">Trace an app</span>
              <div className="seg small" role="tablist" aria-label="Language">
                {Object.keys(snippets).map((l) => (
                  <button key={l} role="tab" aria-selected={lang === l} className={lang === l ? 'on' : ''} onClick={() => setLang(l)}>
                    {l}
                  </button>
                ))}
              </div>
            </div>
            <div className="tr-code mono">
              {snippets[lang].map(([line, kind], i) => (
                <div key={i} className={kind ? '' : 't-note'}>
                  {line}
                </div>
              ))}
            </div>
          </div>
        </section>
      </div>
    </>
  )
}

type MapNode = { name: string; kind: 'router' | 'service' | 'database' | 'external'; rate: number; errors: number; p95_ms: number }
type MapEdge = { from: string; to: string; kind: '' | 'database' | 'external'; rate: number; errors: number; p95_ms: number }

const health = (n: { errors: number; p95_ms: number }) => (n.errors >= 0.05 ? 'failing' : n.p95_ms >= 1000 ? 'slow' : 'healthy')
const perSecond = (r: number) => (r >= 100 ? `${Math.round(r)}/s` : r >= 0.1 ? `${+r.toFixed(1)}/s` : `${+(r * 60).toFixed(1)}/min`)
const share = (e: number) => (e === 0 ? '0%' : e < 0.001 ? '<0.1%' : `${+(e * 100).toFixed(1)}%`)

function HealthMark({ h }: { h: string }) {
  if (h === 'failing') return <FailedMark size={14} />
  if (h === 'slow') return <Warning size={14} color="var(--d-amber)" />
  return <Check size={14} />
}

// ServiceMap draws who calls whom, from the traces: each box says how busy,
// how failing and how slow a service is; each link how often one calls the
// other. Choosing a box finds its traces.
function ServiceMap({ project, from, to, onPick }: { project: string; from: number; to: number; onPick: (query: string) => void }) {
  const m = useData<{ on: boolean; nodes: MapNode[]; edges: MapEdge[] }>(`/api/projects/${encodeURIComponent(project)}/servicemap?from=${from}&to=${to}`)
  const box = useRef<HTMLDivElement>(null)
  const [w, setW] = useState(1000)
  useLayoutEffect(() => {
    const el = box.current
    if (!el) return
    const ro = new ResizeObserver(() => setW(el.clientWidth))
    ro.observe(el)
    setW(el.clientWidth)
    return () => ro.disconnect()
  }, [m.data])
  const nodes = m.data?.nodes ?? []
  const edges = m.data?.edges ?? []
  // Columns by the longest chain of calls into each; databases and
  // servers outside Pier one past what calls them.
  const layout = useMemo(() => {
    const depth = new Map<string, number>()
    for (const n of nodes) depth.set(n.name, 0)
    for (let i = 0; i < nodes.length; i++) for (const e of edges) if (e.from !== e.to) depth.set(e.to, Math.max(depth.get(e.to) ?? 0, (depth.get(e.from) ?? 0) + 1))
    const cols: MapNode[][] = []
    for (const n of [...nodes].sort((a, b) => (a.kind === 'router' ? -1 : b.kind === 'router' ? 1 : a.name.localeCompare(b.name)))) {
      const d = Math.min(depth.get(n.name) ?? 0, 6)
      ;(cols[d] ??= []).push(n)
    }
    return cols.filter(Boolean)
  }, [nodes, edges])
  if (!m.data || !m.data.on || nodes.length === 0) return null
  // Wide, calls run left to right; narrow, top to bottom.
  const down = w < 640
  const BH = 76
  const GAP = 28
  const widest = Math.max(...layout.map((c) => c.length))
  const BW = down ? Math.min(236, (w - (widest - 1) * 12) / widest) : Math.min(236, Math.max(160, (w - 40) / Math.max(1, layout.length) - 64))
  const H = down ? layout.length * BH + (layout.length - 1) * 52 + 24 : widest * BH + (widest - 1) * GAP + 40
  const pos = new Map<string, { x: number; y: number }>()
  layout.forEach((col, ci) => {
    if (down) {
      const left = (w - (col.length * BW + (col.length - 1) * 12)) / 2
      col.forEach((n, ri) => pos.set(n.name, { x: left + ri * (BW + 12), y: 12 + ci * (BH + 52) }))
      return
    }
    const x = layout.length === 1 ? (w - BW) / 2 : 20 + (ci * (w - 40 - BW)) / (layout.length - 1)
    const top = (H - (col.length * BH + (col.length - 1) * GAP)) / 2
    col.forEach((n, ri) => pos.set(n.name, { x, y: top + ri * (BH + GAP) }))
  })
  const ends = (a: { x: number; y: number }, b: { x: number; y: number }) =>
    down ? { x1: a.x + BW / 2, y1: a.y + BH, x2: b.x + BW / 2, y2: b.y - 4 } : { x1: a.x + BW, y1: a.y + BH / 2, x2: b.x - 4, y2: b.y + BH / 2 }
  const query = (n: MapNode) =>
    n.kind === 'database' ? `{ span.db.system = "${n.name}" }` : n.kind === 'external' ? `{ span.server.address = "${n.name}" }` : `{ resource.service.name = "${n.name}" }`
  return (
    <section className="a-section">
      <div className="a-section-head">
        <h2>
          Service map<span>Who calls whom, from the traces</span>
        </h2>
        <div className="a-legend sm-legend">
          <span>
            <Check size={14} />
            Healthy
          </span>
          <span>
            <Warning size={14} color="var(--d-amber)" />
            Slow
          </span>
          <span>
            <FailedMark size={14} />
            Failing
          </span>
        </div>
      </div>
      <div className="panel sm-panel">
        <div className="sm-canvas" ref={box} style={{ height: H }}>
          <svg width={w} height={H} aria-hidden>
            <defs>
              <marker id="sm-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
                <path d="M1 1L9 5L1 9" fill="none" stroke="context-stroke" strokeWidth="1.5" />
              </marker>
            </defs>
            {edges.map((e) => {
              const a = pos.get(e.from)
              const b = pos.get(e.to)
              if (!a || !b) return null
              const { x1, y1, x2, y2 } = ends(a, b)
              const mx = (x1 + x2) / 2
              const my = (y1 + y2) / 2
              const bad = e.errors >= 0.05
              return (
                <path
                  key={e.from + e.to}
                  d={down ? `M${x1} ${y1} C${x1} ${my}, ${x2} ${my}, ${x2} ${y2}` : `M${x1} ${y1} C${mx} ${y1}, ${mx} ${y2}, ${x2} ${y2}`}
                  fill="none"
                  stroke={bad ? 'rgba(240, 122, 122, 0.8)' : 'rgba(255, 255, 255, 0.3)'}
                  strokeWidth="1.25"
                  strokeDasharray={e.kind === 'external' || bad ? '4 4' : undefined}
                  markerEnd="url(#sm-arrow)"
                />
              )
            })}
          </svg>
          {edges.map((e) => {
            const a = pos.get(e.from)
            const b = pos.get(e.to)
            if (!a || !b) return null
            return (
              <span key={'l' + e.from + e.to} className="sm-rate mono" style={{ left: (ends(a, b).x1 + ends(a, b).x2) / 2, top: (ends(a, b).y1 + ends(a, b).y2) / 2 }} title={`${share(e.errors)} errors, p95 ${dur(e.p95_ms)}`}>
                {perSecond(e.rate)}
              </span>
            )
          })}
          {nodes.map((n) => {
            const p = pos.get(n.name)
            if (!p) return null
            const h = health(n)
            return (
              <button key={n.name} className={'sm-node ' + n.kind + (BW < 200 ? ' tight' : '')} style={{ left: p.x, top: p.y, width: BW, height: BH }} onClick={() => onPick(query(n))} title="Find its traces">
                <span className="sm-node-head">
                  <span className="sm-sq" style={{ background: n.kind === 'database' ? '#9085e9' : n.kind === 'external' ? '#199e70' : serviceColor(n.name) }} />
                  <b className="mono">{n.name}</b>
                  {n.kind !== 'service' && <span className="t-note">{n.kind === 'router' ? 'router' : n.kind === 'database' ? 'database' : 'outside Pier'}</span>}
                  <span className="sm-health">
                    <HealthMark h={h} />
                  </span>
                </span>
                <span className="mono sm-line">
                  {perSecond(n.rate)} <span className="t-muted">{share(n.errors)} errors</span>
                </span>
                <span className="mono sm-line t-muted">p95 {dur(n.p95_ms)}</span>
              </button>
            )
          })}
        </div>
      </div>
    </section>
  )
}
