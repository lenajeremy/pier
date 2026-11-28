import { useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { api, type Deployment } from '../lib/api'
import { useRole } from '../lib/role'
import { toast } from '../components/Toast'
import { useData } from '../lib/data'
import { size } from '../lib/format'
import { Calendar, Check, Close, Play, Plus, SortDown } from '../components/Icons'
import { Menu } from '../components/Menu'
import { Problem, Skeleton } from '../components/ui'
import { TelemetryOff, dur } from './Traces'

// Metrics: what a project's apps measure, from Prometheus. The builder
// writes PromQL from a metric, filters and steps; Code is the PromQL
// itself. Either way pierd limits the query to the project's own series.

export type Family = { name: string; type: 'counter' | 'gauge' | 'histogram' | 'summary'; unit?: string }
export type Series = { labels: Record<string, string>; values: [number, number | null][] }
export type Result = { query: string; step: number; series: Series[] }

type Filter = { label: string; op: '=' | '!=' | '=~' | '!~'; value: string }
type Step =
  | { kind: 'rate'; window: string }
  | { kind: 'agg'; fn: Fn; by: string[] }
  | { kind: 'quantile'; q: number }
  | { kind: 'average' }

export type Built = { metric: string; filters: Filter[]; steps: Step[] }

const windows: [string, string][] = [
  ['1m', '1 min'],
  ['5m', '5 min'],
  ['15m', '15 min'],
  ['1h', '1 hour'],
]
type Fn = 'sum' | 'avg' | 'max' | 'min'
const fns: [Fn, string][] = [
  ['sum', 'Sum'],
  ['avg', 'Average'],
  ['max', 'Max'],
  ['min', 'Min'],
]
const quantiles = [0.5, 0.9, 0.95, 0.99]

export function defaults(f: Family): Step[] {
  switch (f.type) {
    case 'counter':
      return [{ kind: 'rate', window: '5m' }, { kind: 'agg', fn: 'sum', by: [] }]
    case 'histogram':
      return [{ kind: 'rate', window: '5m' }, { kind: 'agg', fn: 'sum', by: [] }, { kind: 'quantile', q: 0.95 }]
    case 'summary':
      return [{ kind: 'rate', window: '5m' }, { kind: 'average' }]
    default:
      return [{ kind: 'agg', fn: 'avg', by: [] }]
  }
}

const quote = (v: string) => JSON.stringify(v)

// promql writes the builder's query.
export function promql(b: Built, f: Family | undefined): string {
  if (!b.metric) return ''
  const rate = b.steps.find((s) => s.kind === 'rate') as (Step & { kind: 'rate' }) | undefined
  const agg = b.steps.find((s) => s.kind === 'agg') as (Step & { kind: 'agg' }) | undefined
  const quantile = b.steps.find((s) => s.kind === 'quantile') as (Step & { kind: 'quantile' }) | undefined
  const average = b.steps.some((s) => s.kind === 'average')
  const matchers = b.filters.map((x) => `${x.label}${x.op}${quote(x.value)}`).join(', ')
  const sel = (name: string) => `${name}${matchers ? `{${matchers}}` : ''}`
  const ranged = (name: string) => (rate ? `rate(${sel(name)}[${rate.window}])` : sel(name))
  const by = (extra: string[]) => {
    const labels = [...extra, ...(agg?.by ?? [])]
    return labels.length ? ` by (${labels.join(', ')})` : ''
  }
  const fn = agg?.fn ?? 'sum'
  if ((f?.type === 'histogram' || f?.type === 'summary') && average) {
    return `${fn}${by([])} (${ranged(b.metric + '_sum')}) / ${fn}${by([])} (${ranged(b.metric + '_count')})`
  }
  if (f?.type === 'histogram' && quantile) {
    return `histogram_quantile(${quantile.q}, ${fn}${by(['le'])} (${ranged(b.metric + '_bucket')}))`
  }
  const name = f?.type === 'histogram' || f?.type === 'summary' ? b.metric + '_count' : b.metric
  return agg ? `${fn}${by([])} (${ranged(name)})` : ranged(name)
}

// unitOf says how to read a result: a duration, bytes, a share, a rate.
export function unitOf(b: Built, f: Family | undefined) {
  const quantile = b.steps.some((s) => s.kind === 'quantile' || s.kind === 'average')
  const rate = b.steps.some((s) => s.kind === 'rate')
  if (f && (quantile || !rate)) return f.unit ?? ''
  return rate ? 'per second' : ''
}

export function format(v: number | null | undefined, unit: string) {
  if (v == null) return '–'
  switch (unit) {
    case 'seconds':
      return dur(v * 1000)
    case 'milliseconds':
      return dur(v)
    case 'microseconds':
      return dur(v / 1000)
    case 'nanoseconds':
      return dur(v / 1e6)
    case 'bytes':
      return size(Math.round(v))
    case 'ratio':
      return `${+(v * 100).toFixed(1)}%`
    case 'percent':
      return `${+v.toFixed(1)}%`
    case 'per second':
      return `${number(v)}/s`
  }
  return number(v)
}

function number(v: number) {
  const a = Math.abs(v)
  if (a >= 1e9) return `${+(v / 1e9).toFixed(1)}G`
  if (a >= 1e6) return `${+(v / 1e6).toFixed(1)}M`
  if (a >= 1e4) return `${+(v / 1e3).toFixed(1)}k`
  if (a >= 100) return Math.round(v).toLocaleString()
  if (a >= 1) return `${+v.toFixed(2)}`
  if (a === 0) return '0'
  return `${+v.toPrecision(2)}`
}

export const colors = ['#3987e5', '#d55181', '#9085e9', '#199e70', '#c98a2e', '#4fa3b8', '#b5577a', '#6f8bd6']

const ranges = [
  { key: '15m', label: 'Last 15 minutes', ms: 15 * 60_000 },
  { key: '1h', label: 'Last hour', ms: 3_600_000 },
  { key: '6h', label: 'Last 6 hours', ms: 6 * 3_600_000 },
  { key: '24h', label: 'Last 24 hours', ms: 86_400_000 },
  { key: '7d', label: 'Last 7 days', ms: 7 * 86_400_000 },
]

export function Metrics({ project, tabs }: { project: string; tabs: (right?: ReactNode) => ReactNode }) {
  const base = `/api/projects/${encodeURIComponent(project)}/metrics`
  const list = useData<{ on: boolean; metrics: Family[] }>(base)
  const [range, setRange] = useState(ranges[1])
  const [mode, setMode] = useState<'builder' | 'code'>(() => (new URLSearchParams(location.search).get('q') ? 'code' : 'builder'))
  const [built, setBuilt] = useState<Built>({ metric: '', filters: [], steps: [] })
  const [code, setCode] = useState(() => new URLSearchParams(location.search).get('q') ?? '')
  const [ran, setRan] = useState<{ q: string; unit: string; title: string; note: string } | null>(null)
  const families = list.data?.metrics ?? []
  const family = families.find((f) => f.name === built.metric)
  // Start on the first histogram, the likeliest response time.
  useEffect(() => {
    if (built.metric || !families.length || code) return
    const f = families.find((x) => x.type === 'histogram' && /server.*duration/.test(x.name)) ?? families.find((x) => x.type === 'histogram') ?? families[0]
    setBuilt({ metric: f.name, filters: [], steps: defaults(f) })
  }, [families, built.metric, code])
  const query = mode === 'builder' ? promql(built, family) : code.trim()
  const describe = () => {
    if (mode === 'code' || !family) return { title: 'Query', note: '' }
    const q = built.steps.find((s) => s.kind === 'quantile') as { q: number } | undefined
    const agg = built.steps.find((s) => s.kind === 'agg') as (Step & { kind: 'agg' }) | undefined
    const what = q ? `p${Math.round(q.q * 100)}` : built.steps.some((s) => s.kind === 'average') ? 'average' : built.steps.some((s) => s.kind === 'rate') ? 'per second' : agg ? fns.find(([k]) => k === agg.fn)?.[1].toLowerCase() : ''
    const note = [what, agg?.by.length ? `by ${agg.by.join(', ')}` : ''].filter(Boolean).join(', ')
    return { title: family.name, note: note && note[0].toUpperCase() + note.slice(1) }
  }
  const run = () => {
    if (!query) return
    setRan({ q: query, unit: mode === 'builder' ? unitOf(built, family) : guessUnit(query, families), ...describe() })
    const url = new URL(location.href)
    url.searchParams.set('q', query)
    history.replaceState(null, '', url.pathname + url.search)
  }
  // Run the builder's query as it changes; code runs on Run.
  useEffect(() => {
    if (mode === 'builder' && query) run()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mode, query])
  useEffect(() => {
    if (mode === 'code' && code && !ran) run()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const { change } = useRole()
  const boards = useData<{ dashboards: { id: number; name: string }[] }>(change ? `/api/projects/${encodeURIComponent(project)}/dashboards` : null)
  // A panel from the query as it ran, on the end of a dashboard.
  const addTo = async (id: number, name: string) => {
    if (!ran) return
    const url = `/api/projects/${encodeURIComponent(project)}/dashboards/${id}`
    try {
      const d = await api<{ name: string; spec: { panels: unknown[]; variables?: unknown[] } }>(url)
      const panel = {
        id: Math.random().toString(36).slice(2, 9), title: ran.title === 'Query' ? 'Query' : ran.note ? `${ran.title}, ${ran.note.toLowerCase()}` : ran.title, kind: 'timeseries',
        queries: [{ id: Math.random().toString(36).slice(2, 9), name: 'A', source: 'metrics', mode: mode, expr: ran.q, ...(mode === 'builder' ? { built } : {}) }],
        unit: ran.unit, legend: 'list', stats: ['mean', 'max', 'last'], reduce: 'last', deploys: true, span: 2,
      }
      await api(url, { method: 'PUT', body: JSON.stringify({ name: d.name, spec: { ...d.spec, panels: [...d.spec.panels, panel] } }) })
      toast(`Added to ${name}`)
    } catch (e) {
      toast(e instanceof Error ? e.message : 'Couldn’t add it', { tone: 'failed' })
    }
  }
  const right = (
    <>
      {change && ran && (boards.data?.dashboards.length ?? 0) > 0 && (
        <Menu label="Add to a dashboard" align="end" className="btn quiet medium" items={boards.data!.dashboards.map((b) => ({ label: b.name, onSelect: () => addTo(b.id, b.name) }))}>
          <Plus size={12} color="currentColor" />
          Add to a dashboard
        </Menu>
      )}
      <Menu label="Time range" align="end" className="btn quiet medium" items={ranges.map((r) => ({ label: r.label, onSelect: () => setRange(r) }))}>
        <Calendar />
        {range.label}
      </Menu>
    </>
  )
  if (list.data && !list.data.on) {
    return (
      <main className="main" style={{ gap: 28 }}>
        {tabs()}
        <TelemetryOff project={project} onOn={list.reload} />
      </main>
    )
  }
  return (
    <main className="main" style={{ gap: 28 }}>
      {tabs(right)}
      {list.error ? (
        <Problem>{list.error.message}</Problem>
      ) : !list.data ? (
        <div className="panel">
          <Skeleton rows={3} tall />
        </div>
      ) : families.length === 0 && !code ? (
        <div className="panel a-empty">
          <div className="t-small">No metrics yet</div>
          <div className="t-caption t-muted">Apps send them through OpenTelemetry, and they show here within a minute.</div>
        </div>
      ) : (
        <>
          <section className="a-section">
            <div className="a-section-head">
              <h2>Query</h2>
              {mode === 'code' && (
                <button className="btn quiet medium" onClick={run} disabled={!query}>
                  <Play size={13} />
                  Run
                </button>
              )}
            </div>
            <div className="panel m-query">
              <div className="m-query-head">
                <span className="m-letter">A</span>
                <span className="m-what">
                  {mode === 'code' ? (
                    'PromQL'
                  ) : (
                    <>
                      {describe().note || 'Values'}
                      <span className="t-muted"> of </span>
                      <span className="mono">{built.metric}</span>
                    </>
                  )}
                </span>
                <div className="seg small" role="tablist" aria-label="Write the query with">
                  <button role="tab" aria-selected={mode === 'builder'} className={mode === 'builder' ? 'on' : ''} onClick={() => setMode('builder')}>
                    Builder
                  </button>
                  <button
                    role="tab"
                    aria-selected={mode === 'code'}
                    className={mode === 'code' ? 'on' : ''}
                    onClick={() => {
                      if (mode === 'builder') setCode(query)
                      setMode('code')
                    }}
                  >
                    Code
                  </button>
                </div>
              </div>
              {mode === 'builder' ? (
                <Builder project={project} families={families} built={built} setBuilt={setBuilt} family={family} />
              ) : (
                <textarea
                  className="m-code mono"
                  value={code}
                  spellCheck={false}
                  rows={Math.min(8, Math.max(2, code.split('\n').length))}
                  placeholder="sum by (http_route) (rate(http_server_request_duration_seconds_count[5m]))"
                  onChange={(e) => setCode(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) run()
                  }}
                />
              )}
              {mode === 'builder' && query && <div className="m-promql mono">{query}</div>}
            </div>
          </section>
          {ran && <Results project={project} ran={ran} range={range.ms} />}
        </>
      )}
    </main>
  )
}

export function guessUnit(q: string, families: Family[]) {
  const f = families.find((x) => q.includes(x.name))
  if (!f) return ''
  if (/histogram_quantile|_sum\b.*\/.*_count/.test(q)) return f.unit ?? ''
  if (/\b(rate|irate)\(/.test(q)) return 'per second'
  return f.unit ?? ''
}

function stepLabel(s: Step) {
  switch (s.kind) {
    case 'rate':
      return `Rate over ${windows.find(([k]) => k === s.window)?.[1] ?? s.window}`
    case 'agg':
      return `${fns.find(([k]) => k === s.fn)?.[1]}${s.by.length ? ` by ${s.by.join(', ')}` : ''}`
    case 'quantile':
      return `${Math.round(s.q * 100)}th percentile`
    case 'average':
      return 'Average'
  }
}

export function Builder({ project, families, built, setBuilt, family, noSteps }: {
  project: string
  families: Family[]
  built: Built
  setBuilt: (b: Built) => void
  family?: Family
  noSteps?: boolean // a panel that reads a histogram's buckets as they are
}) {
  const labels = useData<{ labels: Record<string, string[]> }>(
    built.metric ? `/api/projects/${encodeURIComponent(project)}/metrics/labels?metric=${encodeURIComponent(family?.type === 'histogram' ? built.metric + '_bucket' : family?.type === 'summary' ? built.metric + '_count' : built.metric)}` : null,
  )
  const names = Object.keys(labels.data?.labels ?? {}).filter((l) => l !== 'le' && l !== 'quantile')
  const [label, setLabel] = useState<string | null>(null)
  const setSteps = (steps: Step[]) => setBuilt({ ...built, steps })
  const replace = (i: number, s: Step | null) => setSteps(s ? built.steps.map((x, j) => (j === i ? s : x)) : built.steps.filter((_, j) => j !== i))
  const has = (k: Step['kind']) => built.steps.some((s) => s.kind === k)
  const order: Record<Step['kind'], number> = { rate: 0, agg: 1, quantile: 2, average: 2 }
  const add = (s: Step) => setSteps([...built.steps, s].sort((a, b) => order[a.kind] - order[b.kind]))
  const hist = family?.type === 'histogram' || family?.type === 'summary'
  const addable: { label: string; onSelect: () => void }[] = []
  if (!has('rate')) addable.push({ label: 'Rate', onSelect: () => add({ kind: 'rate', window: '5m' }) })
  if (!has('agg')) addable.push({ label: 'Sum, average, max or min', onSelect: () => add({ kind: 'agg', fn: 'sum', by: [] }) })
  if (family?.type === 'histogram' && !has('quantile') && !has('average')) addable.push({ label: 'Percentile', onSelect: () => add({ kind: 'quantile', q: 0.95 }) })
  if (hist && !has('quantile') && !has('average')) addable.push({ label: 'Average', onSelect: () => add({ kind: 'average' }) })
  return (
    <div className="m-builder">
      <span className="m-row-label">Metric</span>
      <div>
        <Picker
          label="Metric"
          className="m-metric mono"
          items={families.map((f) => ({ key: f.name, label: f.name, hint: f.type }))}
          onPick={(name) => {
            const f = families.find((x) => x.name === name)!
            setBuilt({ metric: name, filters: [], steps: defaults(f) })
          }}
        >
          <span>{built.metric || 'Pick a metric'}</span>
          {family && <span className="t-note m-type">{family.type}</span>}
          <SortDown color="currentColor" />
        </Picker>
      </div>
      <span className="m-row-label">Filter</span>
      <div className="m-chips">
        {built.filters.map((x, i) => (
          <span key={i} className="m-chip mono">
            <Menu
              label="Match"
              align="start"
              className="m-chip-op"
              items={(['=', '!=', '=~', '!~'] as const).map((op) => ({ label: { '=': 'is', '!=': "isn't", '=~': 'matches', '!~': "doesn't match" }[op], hint: op, onSelect: () => setBuilt({ ...built, filters: built.filters.map((y, j) => (j === i ? { ...y, op } : y)) }) }))}
            >
              {x.label} <span className="t-note">{x.op}</span> {x.value}
            </Menu>
            <button aria-label="Remove the filter" onClick={() => setBuilt({ ...built, filters: built.filters.filter((_, j) => j !== i) })}>
              <Close size={11} />
            </button>
          </span>
        ))}
        {label ? (
          <Picker
            key={'value:' + label}
            label={`${label} is`}
            open
            className="m-chip mono"
            items={(labels.data?.labels[label] ?? []).map((v) => ({ key: v, label: v }))}
            onClose={() => setLabel(null)}
            onPick={(value) => {
              setBuilt({ ...built, filters: [...built.filters, { label, op: '=', value }] })
              setLabel(null)
            }}
          >
            {label} =
          </Picker>
        ) : (
          <Picker key="label" label="Filter by" className="m-add" items={names.map((n) => ({ key: n, label: n, hint: `${labels.data!.labels[n].length}` }))} onPick={setLabel}>
            <Plus size={12} />
            Filter
          </Picker>
        )}
      </div>
      {!noSteps && (
        <>
      <span className="m-row-label">Then</span>
      <div className="m-chips">
        {built.steps.map((s, i) => (
          <span key={s.kind} className="m-step">
            {i > 0 && <span className="m-arrow">→</span>}
            <span className="m-chip">
              <Menu label="Change the step" align="start" className="m-chip-op" items={stepItems(s, names, (n) => replace(i, n))}>
                {stepLabel(s)}
              </Menu>
              <button aria-label="Remove the step" onClick={() => replace(i, null)}>
                <Close size={11} />
              </button>
            </span>
          </span>
        ))}
        {addable.length > 0 && (
          <Menu label="Add a step" align="start" className="m-add" items={addable}>
            <Plus size={12} />
            Step
          </Menu>
        )}
      </div>
        </>
      )}
    </div>
  )
}

function stepItems(s: Step, labels: string[], set: (s: Step) => void) {
  const tick = (on: boolean) => (on ? <Check size={13} color="currentColor" /> : <span style={{ width: 13 }} />)
  switch (s.kind) {
    case 'rate':
      return windows.map(([k, l]) => ({ label: `Over ${l}`, icon: tick(s.window === k), onSelect: () => set({ ...s, window: k }) }))
    case 'quantile':
      return quantiles.map((q) => ({ label: `${Math.round(q * 100)}th percentile`, icon: tick(s.q === q), onSelect: () => set({ ...s, q }) }))
    case 'average':
      return [{ label: 'Average', icon: tick(true), onSelect: () => set(s) }]
    case 'agg':
      return [
        ...fns.map(([k, l]) => ({ label: l, icon: tick(s.fn === k), onSelect: () => set({ ...s, fn: k }) })),
        ...(labels.length ? (['separator'] as const) : []),
        ...labels.map((l) => ({
          label: `By ${l}`,
          icon: tick(s.by.includes(l)),
          onSelect: () => set({ ...s, by: s.by.includes(l) ? s.by.filter((x) => x !== l) : [...s.by, l] }),
        })),
      ]
  }
}

// Picker is a button that opens a searchable list.
export function Picker({ label, items, onPick, onClose, open: startOpen, className, children }: {
  label: string
  items: { key: string; label: string; hint?: string }[]
  onPick: (key: string) => void
  onClose?: () => void
  open?: boolean
  className?: string
  children: ReactNode
}) {
  const [open, setOpen] = useState(!!startOpen)
  const [find, setFind] = useState('')
  const [at, setAt] = useState<{ left: number; top: number; width: number } | null>(null)
  const button = useRef<HTMLButtonElement>(null)
  const pop = useRef<HTMLDivElement>(null)
  useLayoutEffect(() => {
    if (!open || !button.current) return
    const r = button.current.getBoundingClientRect()
    setAt({ left: r.left, top: r.bottom + 6, width: Math.max(260, r.width) })
  }, [open])
  useEffect(() => {
    if (!open) return
    const away = (e: MouseEvent) => {
      if (!pop.current?.contains(e.target as Node) && !button.current?.contains(e.target as Node)) close()
    }
    const key = (e: KeyboardEvent) => e.key === 'Escape' && close()
    document.addEventListener('mousedown', away)
    document.addEventListener('keydown', key)
    return () => (document.removeEventListener('mousedown', away), document.removeEventListener('keydown', key))
  })
  const close = () => {
    setOpen(false)
    setFind('')
    onClose?.()
  }
  const shown = items.filter((i) => i.label.toLowerCase().includes(find.toLowerCase())).slice(0, 200)
  return (
    <>
      <button ref={button} className={className} aria-label={label} aria-expanded={open} onClick={() => (open ? close() : setOpen(true))}>
        {children}
      </button>
      {open &&
        at &&
        createPortal(
          <div ref={pop} className="m-pop" style={{ left: at.left, top: at.top, minWidth: at.width }}>
            <input autoFocus placeholder={`Find ${label.toLowerCase()}`} value={find} onChange={(e) => setFind(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && shown[0] && (onPick(shown[0].key), close())} />
            <div className="m-pop-list">
              {shown.length === 0 && <div className="m-pop-none">Nothing matches</div>}
              {shown.map((i) => (
                <button
                  key={i.key}
                  onClick={() => {
                    onPick(i.key)
                    setOpen(false)
                    setFind('')
                  }}
                >
                  <span className="mono">{i.label}</span>
                  {i.hint && <span className="t-note">{i.hint}</span>}
                </button>
              ))}
            </div>
          </div>,
          document.body,
        )}
    </>
  )
}

function Results({ project, ran, range }: { project: string; ran: { q: string; unit: string; title: string; note: string }; range: number }) {
  const [view, setView] = useState<'graph' | 'table'>('graph')
  const [to, setTo] = useState(() => Date.now())
  useEffect(() => {
    setTo(Date.now())
    const t = window.setInterval(() => setTo(Date.now()), 30_000)
    return () => window.clearInterval(t)
  }, [ran.q, range])
  const from = to - range
  const sec = (t: number) => t - (t % 1000)
  const q = new URLSearchParams({ q: ran.q, from: String(sec(from)), to: String(sec(to)) })
  const res = useData<{ result: Result }>(`/api/projects/${encodeURIComponent(project)}/metrics/query?${q}`)
  const deps = useData<{ deployments: Deployment[] }>(`/api/projects/${encodeURIComponent(project)}/deployments`)
  const deploys = (deps.data?.deployments ?? []).filter((d) => d.target === 'production' && d.status === 'ready').map((d) => ({ id: d.id, at: Date.parse(d.created_at) + (d.build_ms || 0) }))
  const series = res.data?.result.series ?? []
  const varying = useMemo(() => {
    const keys = new Set(series.flatMap((s) => Object.keys(s.labels)))
    return [...keys].filter((k) => new Set(series.map((s) => s.labels[k] ?? '')).size > 1)
  }, [series])
  const name = (s: Series) => (varying.length ? varying.map((k) => s.labels[k] ?? '').join(', ') : Object.keys(s.labels).length ? Object.entries(s.labels).map(([k, v]) => `${k}=${v}`).join(', ') : ran.title)
  const stats = series.map((s) => {
    const vals = s.values.map(([, v]) => v).filter((v): v is number => v != null)
    return { mean: vals.length ? vals.reduce((a, b) => a + b, 0) / vals.length : null, max: vals.length ? Math.max(...vals) : null, last: vals.length ? vals[vals.length - 1] : null }
  })
  return (
    <section className="a-section">
      <div className="a-section-head">
        <h2>
          {ran.title}
          {ran.note && <span>{ran.note}</span>}
        </h2>
        <div className="seg small" role="tablist" aria-label="Show as">
          <button role="tab" aria-selected={view === 'graph'} className={view === 'graph' ? 'on' : ''} onClick={() => setView('graph')}>
            Graph
          </button>
          <button role="tab" aria-selected={view === 'table'} className={view === 'table' ? 'on' : ''} onClick={() => setView('table')}>
            Table
          </button>
        </div>
      </div>
      {res.error ? (
        <div className="panel m-error mono">{res.error.message}</div>
      ) : !res.data ? (
        <div className="panel">
          <Skeleton rows={4} tall />
        </div>
      ) : series.length === 0 ? (
        <div className="panel a-empty">
          <div className="t-small">No data in this range</div>
          <div className="t-caption t-muted">The query ran, and matched no series. Try a wider range or fewer filters.</div>
        </div>
      ) : (
        <div className="panel m-result">
          {view === 'graph' && <Lines series={series} names={series.map(name)} from={from} to={to} unit={ran.unit} deploys={deploys} />}
          <div className={'m-legend' + (view === 'table' ? ' alone' : '')}>
            <div className="m-legend-row m-legend-head">
              <span>{varying.length ? varying.join(', ') : 'Series'}</span>
              <span>Mean</span>
              <span>Max</span>
              <span>Last</span>
            </div>
            {series.slice(0, view === 'table' ? 500 : 12).map((s, i) => (
              <div key={i} className="m-legend-row">
                <span className="m-series">
                  <span className="a-dot" style={{ background: colors[i % colors.length] }} />
                  <span className="mono">{name(s)}</span>
                </span>
                <span className="mono t-muted">{format(stats[i].mean, ran.unit)}</span>
                <span className="mono t-muted">{format(stats[i].max, ran.unit)}</span>
                <span className="mono">{format(stats[i].last, ran.unit)}</span>
              </div>
            ))}
            {view === 'graph' && series.length > 12 && <div className="a-foot">{series.length - 12} more series in Table</div>}
          </div>
        </div>
      )}
    </section>
  )
}

const clock = (ms: number, withDay: boolean) =>
  new Date(ms).toLocaleString(undefined, withDay ? { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit', hour12: false } : { hour: '2-digit', minute: '2-digit', hour12: false })

// Lines is a time series chart: one line per series, deploys marked, and a
// crosshair that reads every series at a moment.
export function Lines({ series, names, from, to, unit, deploys, goal, min, bars, height = 220 }: {
  series: Series[]
  names: string[]
  from: number
  to: number
  unit: string
  deploys: { id: string; at: number }[]
  goal?: number // a dashed line at a target
  min?: number // where the axis starts, instead of the lowest value or 0
  bars?: boolean // stacked bars instead of lines
  height?: number
}) {
  const box = useRef<HTMLDivElement>(null)
  const [w, setW] = useState(900)
  const [hover, setHover] = useState<number | null>(null)
  useLayoutEffect(() => {
    const el = box.current
    if (!el) return
    const ro = new ResizeObserver(() => setW(el.clientWidth))
    ro.observe(el)
    setW(el.clientWidth)
    return () => ro.disconnect()
  }, [])
  const H = height
  const shown = series.slice(0, 12)
  const all = shown.flatMap((s) => s.values.map(([, v]) => v).filter((v): v is number => v != null))
  const times = shown[0]?.values.map(([t]) => t) ?? []
  // Stacked bars reach the sum of every series at a moment.
  const stacked = bars ? times.map((t) => shown.reduce((a, s) => a + (s.values.find(([tt]) => tt === t)?.[1] ?? 0), 0)) : []
  const lo = min ?? Math.min(0, ...all)
  const hi = nice(Math.max(...(bars ? stacked : all), goal ?? -Infinity, lo + 1e-9) - lo) + lo
  const x = (t: number) => ((t - from) / (to - from)) * w
  const y = (v: number) => H - ((Math.max(v, lo) - lo) / (hi - lo || 1)) * H
  const withDay = to - from > 26 * 3600_000
  const path = (s: Series) => {
    let d = ''
    let pen = false
    for (const [t, v] of s.values) {
      if (v == null) {
        pen = false
        continue
      }
      d += `${pen ? 'L' : 'M'}${x(t).toFixed(1)} ${y(v).toFixed(1)}`
      pen = true
    }
    return d
  }
  const move = (e: React.MouseEvent) => {
    const r = box.current!.getBoundingClientRect()
    const t = from + ((e.clientX - r.left) / w) * (to - from)
    let best = 0
    times.forEach((tt, i) => Math.abs(tt - t) < Math.abs(times[best] - t) && (best = i))
    setHover(times.length ? best : null)
  }
  const ht = hover != null ? times[hover] : null
  const readings =
    ht != null
      ? shown
          .map((s, i) => ({ i, v: s.values.find(([t]) => t === ht)?.[1] ?? null }))
          .filter((r) => r.v != null)
          .sort((a, b) => (b.v as number) - (a.v as number))
      : []
  return (
    <div className="m-lines">
      <div className="tr-y" style={{ height: H }}>
        {[1, 0.75, 0.5, 0.25, 0].map((f) => (
          <span key={f} style={{ top: H - f * H }}>
            {format(lo + f * (hi - lo), unit)}
          </span>
        ))}
      </div>
      <div className="tr-plot" ref={box} style={{ height: H, cursor: 'default' }} onMouseMove={move} onMouseLeave={() => setHover(null)}>
        <svg width={w} height={H} aria-hidden>
          {[0.25, 0.5, 0.75, 1].map((f) => (
            <line key={f} x1="0" x2={w} y1={H - f * H} y2={H - f * H} className="a-grid" />
          ))}
          <line x1="0" x2={w} y1={H} y2={H} className="a-base" />
          {deploys
            .filter((d) => d.at >= from && d.at <= to)
            .map((d) => (
              <line key={d.id} x1={x(d.at)} x2={x(d.at)} y1="-6" y2={H} stroke="rgba(255,255,255,0.28)" strokeDasharray="3 3" />
            ))}
          {bars
            ? times.map((t, k) => {
                const bw = Math.max(2, (w / Math.max(1, times.length)) * 0.72)
                let base = 0
                return shown.map((s, i) => {
                  const v = s.values[k]?.[1] ?? 0
                  const top = y(base + v)
                  const h = y(base) - top
                  base += v
                  return h > 0 ? <rect key={`${k}-${i}`} x={x(t) - bw / 2} y={top} width={bw} height={h} rx={i === shown.length - 1 ? 1.5 : 0} fill={colors[i % colors.length]} /> : null
                })
              })
            : shown.map((s, i) => <path key={i} d={path(s)} fill="none" stroke={colors[i % colors.length]} strokeWidth="1.75" strokeLinejoin="round" />)}
          {goal != null && <line x1="0" x2={w} y1={y(goal)} y2={y(goal)} stroke="var(--d-amber)" strokeDasharray="6 5" strokeWidth="1.25" />}
          {ht != null && <line x1={x(ht)} x2={x(ht)} y1="0" y2={H} stroke="rgba(255,255,255,0.25)" />}
          {ht != null && readings.map((r) => <circle key={r.i} cx={x(ht)} cy={y(r.v as number)} r="3.5" fill={colors[r.i % colors.length]} stroke="var(--d-panel)" strokeWidth="2" />)}
        </svg>
        {deploys
          .filter((d) => d.at >= from && d.at <= to)
          .map((d) => (
            <span key={d.id} className="m-deploy mono" style={{ left: x(d.at) }}>
              {d.id.slice(0, 6)}
            </span>
          ))}
        {ht != null && readings.length > 0 && (
          <div className={'a-tip' + (x(ht) > w * 0.65 ? ' left' : '')} style={{ left: x(ht), top: 8 }}>
            <div className="a-tip-head">{clock(ht, withDay)}</div>
            {readings.slice(0, 8).map((r) => (
              <div key={r.i}>
                <i style={{ background: colors[r.i % colors.length] }} />
                <span className="m-tip-name">{names[r.i]}</span>
                <b>{format(r.v, unit)}</b>
              </div>
            ))}
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

export function nice(v: number) {
  if (v <= 0) return 1
  const p = 10 ** Math.floor(Math.log10(v))
  for (const m of [1, 1.2, 1.5, 2, 2.5, 3, 4, 5, 6, 8, 10]) if (m * p >= v) return m * p
  return 10 * p
}
