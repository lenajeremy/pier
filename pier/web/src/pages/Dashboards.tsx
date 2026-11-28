import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { api, type Deployment } from '../lib/api'
import { useData } from '../lib/data'
import { useRole } from '../lib/role'
import { Calendar, ChevronLeft, ChevronRight, Close, Plus, Redeploy } from '../components/Icons'
import { Menu } from '../components/Menu'
import { Dialog, confirm } from '../components/Dialog'
import { toast } from '../components/Toast'
import { Problem, Skeleton } from '../components/ui'
import { Builder, Lines, colors, defaults, format, promql, type Built, type Family, type Result, type Series } from './Metrics'
import { Sparkline } from '../components/AnalyticsCharts'
import { TelemetryOff } from './Traces'

// Dashboards: a project's own pages of panels. Each panel runs one or more
// queries, PromQL over metrics or LogQL over logs, and shows them as a
// chart, a number, bars or lines of log. $environment and other variables
// are filled in before a query runs, as are $__range and $__interval.

export type Kind = 'timeseries' | 'bars' | 'stat' | 'bargauge' | 'table' | 'heatmap' | 'histogram' | 'states' | 'logs'
type Reduce = 'last' | 'mean' | 'max' | 'min' | 'sum'
type Query = { id: string; name: string; source: 'metrics' | 'logs'; mode: 'builder' | 'code'; expr: string; built?: Built }
export type Panel = {
  id: string
  title: string
  kind: Kind
  queries: Query[]
  unit: string
  min?: number
  goal?: number
  legend: 'table' | 'list' | 'hidden'
  stats: ('mean' | 'max' | 'last' | 'min')[]
  reduce: Reduce
  deploys: boolean
  span: 1 | 2 | 4
}
type Variable = { name: string; values: string[]; metric?: string; label?: string; all: boolean }
type Spec = { panels: Panel[]; variables: Variable[] }
type Board = { id: number; name: string; spec: Spec; updated_at: string; updated_by?: string }

const uid = () => Math.random().toString(36).slice(2, 9)

const kinds: [Kind, string, ReactNode][] = [
  ['timeseries', 'Time series', <path key="t" d="M3 17l5-6 4 3 6-8 3 3" />],
  ['bars', 'Bars', <path key="b" d="M5 20V11M10 20V5M15 20V13M20 20V8" />],
  ['stat', 'Stat', <path key="s" d="M5 8h14M5 13h9M5 18h11" />],
  ['bargauge', 'Bar gauge', <path key="g" d="M4 6h12M4 12h16M4 18h8" />],
  ['table', 'Table', <path key="tb" d="M4 5h16v14H4zM4 10h16M10 10v9" />],
  ['heatmap', 'Heatmap', <path key="h" d="M4 4h6v6H4zM14 4h6v6h-6zM4 14h6v6H4zM14 14h6v6h-6z" />],
  ['histogram', 'Histogram', <path key="hi" d="M4 20V14M9 20V8M14 20V11M19 20V16M3 20h18" />],
  ['states', 'States', <path key="st" d="M4 8h9M15 8h5M4 16h4M10 16h10" />],
  ['logs', 'Logs', <path key="l" d="M8 6h12M8 12h12M8 18h12M4 6h.01M4 12h.01M4 18h.01" />],
]
const units: [string, string][] = [
  ['', 'Number'],
  ['per second', 'Per second'],
  ['milliseconds', 'Milliseconds'],
  ['seconds', 'Seconds'],
  ['bytes', 'Bytes'],
  ['ratio', 'Share, 0 to 1'],
  ['percent', 'Percent'],
]
const reduces: [Reduce, string][] = [
  ['last', 'Last'],
  ['mean', 'Mean'],
  ['max', 'Max'],
  ['min', 'Min'],
  ['sum', 'Total'],
]
const ranges = [
  { label: 'Last 15 minutes', ms: 15 * 60_000 },
  { label: 'Last hour', ms: 3_600_000 },
  { label: 'Last 6 hours', ms: 6 * 3_600_000 },
  { label: 'Last 24 hours', ms: 86_400_000 },
  { label: 'Last 7 days', ms: 7 * 86_400_000 },
]
const refreshes: [number, string][] = [
  [0, 'Off'],
  [10_000, 'Every 10 s'],
  [30_000, 'Every 30 s'],
  [60_000, 'Every minute'],
  [300_000, 'Every 5 min'],
]

const router = (filter = '') => `{service_name="pier", deployment_environment=~"$environment"}${filter}`

// starter is the first dashboard: the router's requests, from Loki, which
// every project with app logs on has.
export function starter(): Spec {
  const q = (name: string, expr: string, source: Query['source'] = 'logs'): Query => ({ id: uid(), name, source, mode: 'code', expr })
  const p = (title: string, kind: Kind, queries: Query[], more: Partial<Panel> = {}): Panel => ({
    id: uid(), title, kind, queries, unit: '', legend: 'list', stats: ['mean', 'max', 'last'], reduce: 'last', deploys: true, span: kind === 'stat' ? 1 : 2, ...more,
  })
  return {
    variables: [{ name: 'environment', values: ['production', 'preview'], all: true }],
    panels: [
      p('Requests', 'stat', [q('Requests', `sum(count_over_time(${router()} [$__interval]))`)], { reduce: 'sum' }),
      p('Error rate', 'stat', [q('Errors', `sum(count_over_time(${router(' | http_response_status_code >= 500')} [$__interval])) / sum(count_over_time(${router()} [$__interval]))`)], { unit: 'ratio', reduce: 'mean', goal: 0.01 }),
      p('Response time, p95', 'stat', [q('p95', `max(quantile_over_time(0.95, ${router(' | unwrap duration_ms')} [$__interval]) by (service_name))`)], { unit: 'milliseconds', reduce: 'mean' }),
      p('App errors', 'stat', [q('Errors', `sum(count_over_time({deployment_environment=~"$environment", service_name!="pier"} | detected_level="error" [$__interval]))`)], { reduce: 'sum' }),
      p('Response time', 'timeseries', [
        q('p50', `max(quantile_over_time(0.5, ${router(' | unwrap duration_ms')} [$__interval]) by (service_name))`),
        q('p95', `max(quantile_over_time(0.95, ${router(' | unwrap duration_ms')} [$__interval]) by (service_name))`),
      ], { unit: 'milliseconds' }),
      p('Requests by status', 'bars', [q('Requests', `sum by (http_response_status_code) (count_over_time(${router()} [$__interval]))`)], { legend: 'list' }),
      p('Busiest paths', 'bargauge', [q('Requests', `topk(8, sum by (url_path) (count_over_time(${router()} [$__range])))`)], { reduce: 'last' }),
      p('Errors', 'logs', [q('Errors', `{deployment_environment=~"$environment", service_name=~".+"} | detected_level="error"`)]),
    ],
  }
}

export function Dashboards({ project, tabs }: { project: string; tabs: (right?: ReactNode) => ReactNode }) {
  const base = `/api/projects/${encodeURIComponent(project)}/dashboards`
  const list = useData<{ dashboards: { id: number; name: string }[] }>(base)
  const telemetry = useData<{ status?: { on: boolean } }>('/api/server/telemetry')
  const { change } = useRole()
  const [id, setId] = useState<number | null>(() => Number(new URLSearchParams(location.search).get('dashboard')) || null)
  const [making, setMaking] = useState(false)
  const boards = list.data?.dashboards ?? []
  const current = id && boards.some((b) => b.id === id) ? id : boards[0]?.id ?? null
  const pick = (n: number | null) => {
    setId(n)
    const url = new URL(location.href)
    if (n) url.searchParams.set('dashboard', String(n))
    else url.searchParams.delete('dashboard')
    history.replaceState(null, '', url.pathname + url.search)
  }
  if (telemetry.data && !telemetry.data.status?.on) {
    return (
      <main className="main" style={{ gap: 28 }}>
        {tabs()}
        <TelemetryOff project={project} onOn={telemetry.reload} />
      </main>
    )
  }
  return (
    <main className="main" style={{ gap: 24 }}>
      {tabs()}
      {list.error ? (
        <Problem>{list.error.message}</Problem>
      ) : !list.data ? (
        <div className="panel">
          <Skeleton rows={4} tall />
        </div>
      ) : current == null ? (
        <div className="panel a-empty">
          <div className="t-small">No dashboards yet</div>
          <div className="t-caption t-muted">Start with requests, errors and response times, then add panels of your own.</div>
          {change && (
            <button className="btn primary medium" style={{ marginTop: 12 }} onClick={() => setMaking(true)}>
              Make a dashboard
            </button>
          )}
        </div>
      ) : (
        <Dashboard key={current} project={project} id={current} boards={boards} onPick={pick} onMake={() => setMaking(true)} onDeleted={() => (pick(null), list.reload())} onRenamed={list.reload} />
      )}
      {making && (
        <MakeDashboard
          base={base}
          first={boards.length === 0}
          onClose={() => setMaking(false)}
          onMade={(n) => {
            setMaking(false)
            list.reload()
            pick(n)
          }}
        />
      )}
    </main>
  )
}

function MakeDashboard({ base, first, onClose, onMade }: { base: string; first: boolean; onClose: () => void; onMade: (id: number) => void }) {
  const [name, setName] = useState(first ? 'Requests' : '')
  const [start, setStart] = useState(first)
  const [busy, setBusy] = useState(false)
  const [problem, setProblem] = useState('')
  const submit = async () => {
    setBusy(true)
    setProblem('')
    try {
      const d = await api<{ id: number }>(base, { method: 'POST', body: JSON.stringify({ name, spec: start ? starter() : { panels: [], variables: [] } }) })
      onMade(d.id)
    } catch (e) {
      setProblem(e instanceof Error ? e.message : 'Couldn’t make it')
      setBusy(false)
    }
  }
  return (
    <Dialog
      title="Make a dashboard"
      wide
      busy={busy}
      onClose={onClose}
      onSubmit={(e) => (e.preventDefault(), submit())}
      footer={
        <>
          <button type="button" className="btn quiet medium" onClick={onClose}>
            Cancel
          </button>
          <button className="btn primary medium" disabled={busy || !name.trim()}>
            {busy ? 'Making' : 'Make'}
          </button>
        </>
      }
    >
      <label className="field">
        <span className="t-small">Name</span>
        <input className="input" autoFocus value={name} onChange={(e) => setName(e.target.value)} maxLength={80} />
      </label>
      <label className="d-check">
        <input type="checkbox" checked={start} onChange={(e) => setStart(e.target.checked)} />
        <span>Start with requests, errors, response times and the busiest paths</span>
      </label>
      {problem && <Problem>{problem}</Problem>}
    </Dialog>
  )
}

function Dashboard({ project, id, boards, onPick, onMake, onDeleted, onRenamed }: {
  project: string
  id: number
  boards: { id: number; name: string }[]
  onPick: (id: number) => void
  onMake: () => void
  onDeleted: () => void
  onRenamed: () => void
}) {
  const url = `/api/projects/${encodeURIComponent(project)}/dashboards/${id}`
  const d = useData<Board>(url)
  const { change } = useRole()
  const [spec, setSpec] = useState<Spec | null>(null)
  const [name, setName] = useState('')
  const [editing, setEditing] = useState(false)
  const [panel, setPanel] = useState<Panel | null>(null)
  const [range, setRange] = useState(ranges[1])
  const [shift, setShift] = useState(0)
  const [refresh, setRefresh] = useState(refreshes[2])
  const [now, setNow] = useState(() => Date.now())
  const [vars, setVars] = useState<Record<string, string>>({})
  useEffect(() => {
    if (d.data && !spec) {
      setSpec({ panels: d.data.spec.panels ?? [], variables: d.data.spec.variables ?? [] })
      setName(d.data.name)
    }
  }, [d.data, spec])
  useEffect(() => {
    if (!refresh[0] || shift) return
    const t = window.setInterval(() => setNow(Date.now()), refresh[0])
    return () => window.clearInterval(t)
  }, [refresh, shift])
  const deps = useData<{ deployments: Deployment[] }>(`/api/projects/${encodeURIComponent(project)}/deployments`)
  const deploys = (deps.data?.deployments ?? []).filter((x) => x.target === 'production' && x.status === 'ready').map((x) => ({ id: x.id, at: Date.parse(x.created_at) + (x.build_ms || 0) }))
  const to = now - shift * range.ms
  const from = to - range.ms
  const values = useMemo(() => {
    const out: Record<string, string> = {}
    for (const v of spec?.variables ?? []) {
      const picked = vars[v.name] ?? v.values[0] ?? ''
      out[v.name] = picked === '' || picked === '*' ? '.+' : picked
    }
    return out
  }, [spec, vars])

  const save = async (next: Spec, nextName = name) => {
    setSpec(next)
    try {
      await api(url, { method: 'PUT', body: JSON.stringify({ name: nextName, spec: next }) })
      if (nextName !== d.data?.name) onRenamed()
    } catch (e) {
      toast(e instanceof Error ? e.message : 'Couldn’t save the dashboard', { tone: 'failed' })
    }
  }
  if (d.error) return <Problem>{d.error.message}</Problem>
  if (!spec) {
    return (
      <div className="panel">
        <Skeleton rows={4} tall />
      </div>
    )
  }
  if (panel) {
    return (
      <PanelEditor
        project={project}
        dashboard={name}
        start={panel}
        from={from}
        to={to}
        vars={values}
        deploys={deploys}
        onDiscard={() => setPanel(null)}
        onSave={(p) => {
          const exists = spec.panels.some((x) => x.id === p.id)
          save({ ...spec, panels: exists ? spec.panels.map((x) => (x.id === p.id ? p : x)) : [...spec.panels, p] })
          setPanel(null)
        }}
      />
    )
  }
  const move = (i: number, by: number) => {
    const ps = [...spec.panels]
    const [p] = ps.splice(i, 1)
    ps.splice(Math.max(0, Math.min(ps.length, i + by)), 0, p)
    save({ ...spec, panels: ps })
  }
  const blank = (): Panel => ({
    id: uid(), title: 'New panel', kind: 'timeseries', unit: '', legend: 'list', stats: ['mean', 'max', 'last'], reduce: 'last', deploys: true, span: 2,
    queries: [{ id: uid(), name: 'A', source: 'metrics', mode: 'builder', expr: '' }],
  })
  return (
    <>
      <div className="dash-head">
        <div className="dash-title">
          {editing ? (
            <input className="dash-name-input" value={name} maxLength={80} onChange={(e) => setName(e.target.value)} onBlur={() => name.trim() && name !== d.data?.name && save(spec, name.trim())} aria-label="Dashboard name" />
          ) : (
            <Menu
              label="Dashboards"
              align="start"
              className="dash-name"
              items={[
                ...boards.map((b) => ({ label: b.name, onSelect: () => onPick(b.id) })),
                ...(change ? (['separator', { label: 'Make a dashboard', onSelect: onMake }] as const) : []),
              ]}
            >
              <h1>{name}</h1>
              <span className="dash-chev">
                <ChevronRight size={14} />
              </span>
            </Menu>
          )}
        </div>
        {change && (
          <div className="dash-actions">
            {editing && (
              <button
                className="btn quiet medium"
                onClick={async () => {
                  if (await confirm({ title: `Delete ${name}?`, body: 'Its panels go with it. The data they showed stays.', action: 'Delete', danger: true, run: () => api(url, { method: 'DELETE' }) })) onDeleted()
                }}
              >
                Delete
              </button>
            )}
            <button className="btn quiet medium" onClick={() => setEditing(!editing)}>
              {editing ? 'Done' : 'Edit'}
            </button>
            <button className="btn primary medium" onClick={() => setPanel(blank())}>
              <Plus size={12} color="currentColor" />
              Add panel
            </button>
          </div>
        )}
      </div>
      <div className="dash-bar">
        <div className="dash-vars">
          {spec.variables.map((v) => (
            <Menu
              key={v.name}
              label={v.name}
              align="start"
              className="dash-var"
              items={[...(v.all ? [{ label: 'All', onSelect: () => setVars({ ...vars, [v.name]: '*' }) }] : []), ...v.values.map((x) => ({ label: x, onSelect: () => setVars({ ...vars, [v.name]: x }) }))]}
            >
              <span className="t-muted">{v.name}</span>
              <span className="mono">{(vars[v.name] ?? v.values[0]) === '*' ? 'All' : vars[v.name] ?? v.values[0]}</span>
              {editing && (
                <span
                  className="dash-var-x"
                  role="button"
                  aria-label={`Remove ${v.name}`}
                  onClick={(e) => (e.stopPropagation(), save({ ...spec, variables: spec.variables.filter((x) => x.name !== v.name) }))}
                >
                  <Close size={11} />
                </span>
              )}
            </Menu>
          ))}
          {editing && <AddVariable project={project} onAdd={(v) => save({ ...spec, variables: [...spec.variables.filter((x) => x.name !== v.name), v] })} />}
        </div>
        <div className="dash-time">
          <button className="icon-btn dash-shift" aria-label="Earlier" onClick={() => setShift(shift + 1)}>
            <ChevronLeft size={14} />
          </button>
          <Menu label="Time range" align="end" className="btn quiet medium" items={ranges.map((r) => ({ label: r.label, onSelect: () => (setRange(r), setShift(0), setNow(Date.now())) }))}>
            <Calendar />
            {shift ? `${clock(from)} to ${clock(to)}` : range.label}
          </Menu>
          <button className="icon-btn dash-shift" aria-label="Later" disabled={!shift} onClick={() => (setShift(shift - 1), setNow(Date.now()))}>
            <ChevronRight size={14} />
          </button>
          <Menu label="Refresh" align="end" className="btn quiet medium" items={refreshes.map((r) => ({ label: r[1], onSelect: () => (setRefresh(r), setNow(Date.now())) }))}>
            <Redeploy size={13} />
            {refresh[1]}
          </Menu>
        </div>
      </div>
      {spec.panels.length === 0 ? (
        <div className="panel a-empty">
          <div className="t-small">No panels yet</div>
          <div className="t-caption t-muted">A panel charts a metric, counts log lines, or lists them.</div>
        </div>
      ) : (
        <div className="dash-grid">
          {spec.panels.map((p, i) => (
            <PanelBox
              key={p.id}
              project={project}
              p={p}
              from={from}
              to={to}
              vars={values}
              deploys={deploys}
              menu={
                change
                  ? [
                      { label: 'Edit', onSelect: () => setPanel(p) },
                      { label: 'Duplicate', onSelect: () => save({ ...spec, panels: [...spec.panels.slice(0, i + 1), { ...p, id: uid(), title: p.title + ' copy' }, ...spec.panels.slice(i + 1)] }) },
                      'separator',
                      ...([1, 2, 4] as const).map((s) => ({ label: s === 1 ? 'Quarter width' : s === 2 ? 'Half width' : 'Full width', disabled: p.span === s, onSelect: () => save({ ...spec, panels: spec.panels.map((x) => (x.id === p.id ? { ...x, span: s } : x)) }) })),
                      { label: 'Move earlier', disabled: i === 0, onSelect: () => move(i, -1) },
                      { label: 'Move later', disabled: i === spec.panels.length - 1, onSelect: () => move(i, 1) },
                      'separator',
                      { label: 'Delete', danger: true, onSelect: () => save({ ...spec, panels: spec.panels.filter((x) => x.id !== p.id) }) },
                    ]
                  : undefined
              }
            />
          ))}
        </div>
      )}
    </>
  )
}

function AddVariable({ project, onAdd }: { project: string; onAdd: (v: Variable) => void }) {
  const [open, setOpen] = useState(false)
  const metrics = useData<{ metrics: Family[] }>(open ? `/api/projects/${encodeURIComponent(project)}/metrics` : null)
  const [metric, setMetric] = useState('')
  const labels = useData<{ labels: Record<string, string[]> }>(metric ? `/api/projects/${encodeURIComponent(project)}/metrics/labels?metric=${encodeURIComponent(metric)}` : null)
  if (!open) {
    return (
      <button className="m-add" onClick={() => setOpen(true)}>
        <Plus size={12} />
        Variable
      </button>
    )
  }
  const series = (f: Family) => (f.type === 'histogram' ? f.name + '_bucket' : f.type === 'summary' ? f.name + '_count' : f.name)
  return (
    <span className="dash-addvar">
      <Menu label="From the metric" align="start" className="m-chip m-chip-op mono" items={(metrics.data?.metrics ?? []).map((f) => ({ label: f.name, onSelect: () => setMetric(series(f)) }))}>
        {metric || 'Pick a metric'}
      </Menu>
      {metric && (
        <Menu
          label="Its label"
          align="start"
          className="m-chip m-chip-op mono"
          items={Object.entries(labels.data?.labels ?? {})
            .filter(([k]) => k !== 'le')
            .map(([k, vs]) => ({ label: k, hint: String(vs.length), onSelect: () => (onAdd({ name: k, values: vs, metric, label: k, all: true }), setOpen(false), setMetric('')) }))}
        >
          Pick a label
        </Menu>
      )}
      <button className="m-add" onClick={() => (setOpen(false), setMetric(''))} aria-label="Cancel">
        <Close size={11} />
      </button>
    </span>
  )
}

// interval is the step a panel queries at, in seconds: about 120 points,
// and never under a minute, since apps send metrics every 15 to 60 s.
const interval = (from: number, to: number) => Math.max(60, Math.round((to - from) / 1000 / 120))

// fill puts the variables' values and the time span into a query.
function fill(expr: string, vars: Record<string, string>, from: number, to: number) {
  const step = interval(from, to)
  return expr
    .replace(/\$__range\b/g, `${Math.max(1, Math.round((to - from) / 1000))}s`)
    .replace(/\$__interval\b/g, `${step}s`)
    .replace(/\$(\w+)/g, (m, n: string) => (n in vars ? vars[n] : m))
}

type Ran = { series: Series[]; names: string[] } | { lines: { at: number; labels: Record<string, string>; line: string }[] }

function useRun(project: string, p: Panel, from: number, to: number, vars: Record<string, string>) {
  const [ran, setRan] = useState<Ran | null>(null)
  const [problem, setProblem] = useState('')
  const base = `/api/projects/${encodeURIComponent(project)}`
  const key = JSON.stringify([p.kind, p.title, p.queries.map((q) => [q.name, q.source, q.expr]), from, to, vars])
  useEffect(() => {
    let stopped = false
    const queries = p.queries.filter((q) => q.expr.trim())
    if (!queries.length) {
      setRan(null)
      setProblem('')
      return
    }
    const span = new URLSearchParams({ from: String(Math.floor(from)), to: String(Math.ceil(to)), step: String(interval(from, to)) })
    if (p.kind === 'logs') {
      api<{ lines: { at: number; labels: Record<string, string>; line: string }[] }>(`${base}/logs/lines?q=${encodeURIComponent(fill(queries[0].expr, vars, from, to))}&limit=50&${span}`).then(
        (r) => !stopped && (setRan({ lines: r.lines }), setProblem('')),
        (e) => !stopped && setProblem(e instanceof Error ? e.message : 'Couldn’t run it'),
      )
    } else {
      Promise.all(
        queries.map((q) => api<{ result: Result }>(`${base}/${q.source === 'logs' ? 'logs/metric' : 'metrics/query'}?q=${encodeURIComponent(fill(q.expr, vars, from, to))}&${span}`).then((r) => ({ q, series: r.result.series }))),
      ).then(
        (rs) => {
          if (stopped) return
          const series: Series[] = []
          const names: string[] = []
          for (const { q, series: ss } of rs) {
            const keys = [...new Set(ss.flatMap((s) => Object.keys(s.labels)))].filter((k) => new Set(ss.map((s) => s.labels[k] ?? '')).size > 1)
            for (const s of ss) {
              series.push(s)
              const label = keys.map((k) => s.labels[k] ?? '').join(', ')
              // A lone query keeps its letter only if nothing better names it.
              const qname = rs.length === 1 && /^[A-Z]$/.test(q.name) ? p.title : q.name
              names.push(rs.length > 1 ? (label ? `${q.name} ${label}` : q.name) : label || qname)
            }
          }
          setRan({ series, names })
          setProblem('')
        },
        (e) => !stopped && setProblem(e instanceof Error ? e.message : 'Couldn’t run it'),
      )
    }
    return () => {
      stopped = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key])
  return { ran, problem }
}

function reduce(s: Series, how: Reduce) {
  const vs = s.values.map(([, v]) => v).filter((v): v is number => v != null)
  if (!vs.length) return null
  switch (how) {
    case 'last':
      return vs[vs.length - 1]
    case 'mean':
      return vs.reduce((a, b) => a + b, 0) / vs.length
    case 'max':
      return Math.max(...vs)
    case 'min':
      return Math.min(...vs)
    case 'sum':
      return vs.reduce((a, b) => a + b, 0)
  }
}

// combined adds every series up at each moment, for a stat.
function combined(series: Series[]): Series {
  const at = new Map<number, number | null>()
  for (const s of series)
    for (const [t, v] of s.values) {
      if (v == null) {
        if (!at.has(t)) at.set(t, null)
        continue
      }
      at.set(t, (at.get(t) ?? 0) + v)
    }
  return { labels: {}, values: [...at.entries()].sort((a, b) => a[0] - b[0]) }
}

function PanelBox({ project, p, from, to, vars, deploys, menu, big }: {
  project: string
  p: Panel
  from: number
  to: number
  vars: Record<string, string>
  deploys: { id: string; at: number }[]
  menu?: Parameters<typeof Menu>[0]['items']
  big?: boolean
}) {
  const { ran, problem } = useRun(project, p, from, to, vars)
  const series = ran && 'series' in ran ? ran.series : []
  const names = ran && 'names' in ran ? ran.names : []
  const statsOf = (s: Series) => ({ mean: reduce(s, 'mean'), max: reduce(s, 'max'), last: reduce(s, 'last'), min: reduce(s, 'min') })
  const head = (
    <div className="dash-panel-head">
      <h3>{p.title}</h3>
      {p.legend === 'list' && (p.kind === 'timeseries' || p.kind === 'bars') && series.length > 0 && (
        <div className="a-legend">
          {names.slice(0, 6).map((n, i) => (
            <span key={i}>
              <span className="a-dot" style={{ background: colors[i % colors.length] }} />
              {n}
              {series.length === 1 && p.kind === 'bars' && <b>{format(reduce(series[0], 'sum'), p.unit)}</b>}
            </span>
          ))}
        </div>
      )}
      {menu && <Menu label="Panel" align="end" items={menu} className="icon-btn dash-more" />}
    </div>
  )
  let body: ReactNode
  if (problem) body = <div className="dash-problem mono">{problem}</div>
  else if (!ran) body = p.queries.some((q) => q.expr.trim()) ? <Skeleton rows={3} /> : <div className="dash-problem">Write a query below to see it here.</div>
  else if ('lines' in ran) {
    body = ran.lines.length ? (
      <div className="dash-logs">
        {ran.lines.map((l, i) => (
          <div key={i} className="dash-log">
            <span className="mono t-note">{clock(l.at, true)}</span>
            <span className="mono t-muted">{l.labels.service_name}</span>
            <span className="mono dash-log-text">{message(l.line)}</span>
          </div>
        ))}
      </div>
    ) : (
      <div className="dash-problem">No lines in this range</div>
    )
  } else if (!series.length) body = <div className="dash-problem">No data in this range</div>
  else if (p.kind === 'stat') {
    const c = combined(series)
    const v = reduce(c, p.reduce)
    const over = p.goal != null && v != null && v > p.goal
    body = (
      <div className="dash-stat">
        <div className="a-value">{format(v, p.unit)}</div>
        <div className="a-note">
          {p.goal != null ? (
            <span className="a-state">
              <span className="a-dot" style={{ background: over ? 'var(--d-amber)' : 'var(--d-teal)' }} />
              {over ? 'Over' : 'Under'} the {format(p.goal, p.unit)} goal
            </span>
          ) : (
            `${reduces.find(([k]) => k === p.reduce)?.[1]}, ${rangeName(to - from)}`
          )}
        </div>
        <Sparkline values={c.values.map(([, x]) => x)} />
      </div>
    )
  } else if (p.kind === 'bargauge' || p.kind === 'table') {
    const rows = series.map((s, i) => ({ name: names[i], v: reduce(s, p.reduce), st: statsOf(s), i })).sort((a, b) => (b.v ?? 0) - (a.v ?? 0))
    const max = Math.max(1e-9, ...rows.map((r) => r.v ?? 0))
    body =
      p.kind === 'bargauge' ? (
        <div className="dash-gauge">
          {rows.slice(0, 20).map((r) => (
            <div key={r.i} className="dash-gauge-row">
              <span className="mono dash-gauge-name">{r.name}</span>
              <span className="mono">{format(r.v, p.unit)}</span>
              <span className="a-bar">
                <span style={{ width: `${((r.v ?? 0) / max) * 100}%` }} />
              </span>
            </div>
          ))}
        </div>
      ) : (
        <Legend names={names} series={series} unit={p.unit} stats={p.stats.length ? p.stats : ['mean', 'max', 'last']} alone />
      )
  } else if (p.kind === 'histogram') {
    body = <Histogram series={series} unit={p.unit} height={big ? 240 : 180} />
  } else if (p.kind === 'heatmap') {
    body = <Heatmap series={series} unit={p.unit} height={big ? 240 : 180} />
  } else if (p.kind === 'states') {
    body = <States series={series} names={names} from={from} to={to} goal={p.goal} unit={p.unit} />
  } else {
    body = (
      <>
        <Lines series={series} names={names} from={from} to={to} unit={p.unit} deploys={p.deploys ? deploys : []} goal={p.goal} min={p.min} bars={p.kind === 'bars'} height={big ? 240 : 180} />
        {p.legend === 'table' && <Legend names={names} series={series} unit={p.unit} stats={p.stats} />}
      </>
    )
  }
  return (
    <section className={'panel dash-panel span-' + p.span + (p.kind === 'stat' ? ' stat' : '')}>
      {head}
      {body}
    </section>
  )
}

function Legend({ names, series, unit, stats, alone }: { names: string[]; series: Series[]; unit: string; stats: Panel['stats']; alone?: boolean }) {
  const cols = stats.length ? stats : (['mean', 'max', 'last'] as const)
  return (
    <div className={'m-legend dash-legend' + (alone ? ' alone' : '')} style={{ '--n': cols.length } as React.CSSProperties}>
      <div className="m-legend-row m-legend-head">
        <span>Series</span>
        {cols.map((c) => (
          <span key={c}>{c[0].toUpperCase() + c.slice(1)}</span>
        ))}
      </div>
      {series.slice(0, 50).map((s, i) => (
        <div key={i} className="m-legend-row">
          <span className="m-series">
            <span className="a-dot" style={{ background: colors[i % colors.length] }} />
            <span className="mono">{names[i]}</span>
          </span>
          {cols.map((c) => (
            <span key={c} className={'mono' + (c === 'last' ? '' : ' t-muted')}>
              {format(reduce(s, c), unit)}
            </span>
          ))}
        </div>
      ))}
    </div>
  )
}

function Switch({ on, onChange, label }: { on: boolean; onChange: (v: boolean) => void; label: string }) {
  return (
    <button role="switch" aria-checked={on} aria-label={label} className={'d-switch' + (on ? ' on' : '')} onClick={() => onChange(!on)}>
      <span />
    </button>
  )
}

function PanelEditor({ project, dashboard, start, from, to, vars, deploys, onSave, onDiscard }: {
  project: string
  dashboard: string
  start: Panel
  from: number
  to: number
  vars: Record<string, string>
  deploys: { id: string; at: number }[]
  onSave: (p: Panel) => void
  onDiscard: () => void
}) {
  const [p, setP] = useState<Panel>(start)
  const [openQ, setOpenQ] = useState<string | undefined>(start.queries[start.queries.length - 1]?.id)
  const metrics = useData<{ metrics: Family[] }>(`/api/projects/${encodeURIComponent(project)}/metrics`)
  const families = metrics.data?.metrics ?? []
  const set = (more: Partial<Panel>) => setP((prev) => ({ ...prev, ...more }))
  const setQuery = (id: string, more: Partial<Query>) => setP((prev) => ({ ...prev, queries: prev.queries.map((q) => (q.id === id ? { ...q, ...more } : q)) }))
  // Builder queries write their PromQL as they change.
  useEffect(() => {
    let changed = false
    const queries = p.queries.map((q) => {
      if (q.source !== 'metrics' || q.mode !== 'builder' || !q.built) return q
      const family = families.find((f) => f.name === q.built!.metric)
      const expr = (p.kind === 'heatmap' || p.kind === 'histogram') && family?.type === 'histogram' ? bucketQuery(q.built, p.kind) : promql(q.built, family)
      if (expr === q.expr) return q
      changed = true
      return { ...q, expr }
    })
    if (changed) setP((prev) => ({ ...prev, queries: prev.queries.map((q) => queries.find((x) => x.id === q.id) ?? q) }))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [p.queries, p.kind, families])
  // A new metrics query starts on the first histogram, with its usual steps.
  useEffect(() => {
    if (!families.length) return
    const blank = p.queries.find((q) => q.source === 'metrics' && q.mode === 'builder' && !q.built)
    if (!blank) return
    const f = families.find((x) => x.type === 'histogram' && /server.*duration/.test(x.name)) ?? families[0]
    setQuery(blank.id, { built: { metric: f.name, filters: [], steps: defaults(f) } })
    if (p.unit === '' && f.unit) set({ unit: f.unit })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [families, p.queries])
  const letter = (i: number) => String.fromCharCode(65 + i)
  return (
    <>
      <div className="dash-head">
        <div className="tr-crumb dash-crumb">
          <button onClick={onDiscard} aria-label="Back to the dashboard">
            <ChevronLeft />
          </button>
          <span>{dashboard}</span>
          <span>/</span>
          <b>{p.queries.length && start.title !== 'New panel' ? 'Edit panel' : 'New panel'}</b>
        </div>
        <div className="dash-actions">
          <button className="btn quiet medium" onClick={onDiscard}>
            Discard
          </button>
          <button className="btn primary medium" onClick={() => onSave(p)}>
            Save
          </button>
        </div>
      </div>
      <div className="dash-editor">
        <div className="dash-editor-main">
          <PanelBox project={project} p={{ ...p, span: 4 }} from={from} to={to} vars={vars} deploys={deploys} big />
          <section className="panel dash-queries">
            <div className="dash-queries-head">
              <span className="t-strong">Queries</span>
            </div>
            {p.queries.map((q, i) => {
              const family = families.find((f) => f.name === q.built?.metric)
              const open = openQ === q.id
              return (
                <div key={q.id} className="dash-query">
                  <div className="m-query-head">
                    <span className="m-letter">{letter(i)}</span>
                    <input className="dash-qname" value={q.name} onChange={(e) => setQuery(q.id, { name: e.target.value })} aria-label="Query name" />
                    <div className="seg small" role="tablist" aria-label="Data">
                      {(['metrics', 'logs'] as const).map((s) => (
                        <button key={s} role="tab" aria-selected={q.source === s} className={q.source === s ? 'on' : ''} onClick={() => setQuery(q.id, { source: s, mode: s === 'logs' ? 'code' : q.mode, expr: s === q.source ? q.expr : '' })}>
                          {s === 'metrics' ? 'Metrics' : 'Logs'}
                        </button>
                      ))}
                    </div>
                    {q.source === 'metrics' && (
                      <div className="seg small" role="tablist" aria-label="Write it with">
                        {(['builder', 'code'] as const).map((m) => (
                          <button key={m} role="tab" aria-selected={q.mode === m} className={q.mode === m ? 'on' : ''} onClick={() => setQuery(q.id, { mode: m })}>
                            {m === 'builder' ? 'Builder' : 'Code'}
                          </button>
                        ))}
                      </div>
                    )}
                    <span className="dash-query-end">
                      {p.queries.length > 1 && (
                        <button className="icon-btn" aria-label="Remove the query" onClick={() => set({ queries: p.queries.filter((x) => x.id !== q.id) })}>
                          <Close size={13} />
                        </button>
                      )}
                      <button className={'icon-btn dash-fold' + (open ? ' open' : '')} aria-label={open ? 'Fold' : 'Open'} onClick={() => setOpenQ(open ? undefined : q.id)}>
                        <ChevronRight size={14} />
                      </button>
                    </span>
                  </div>
                  {open &&
                    (q.source === 'metrics' && q.mode === 'builder' ? (
                      q.built ? (
                        <>
                          <Builder project={project} families={families} built={q.built} setBuilt={(b) => setQuery(q.id, { built: b })} family={family} noSteps={(p.kind === 'heatmap' || p.kind === 'histogram') && family?.type === 'histogram'} />
                          {q.expr && <div className="m-promql mono">{q.expr}</div>}
                        </>
                      ) : (
                        <div className="dash-problem">{metrics.data ? 'No metrics yet. Apps send them through OpenTelemetry.' : 'Loading metrics'}</div>
                      )
                    ) : (
                      <textarea
                        className="m-code mono"
                        value={q.expr}
                        spellCheck={false}
                        rows={Math.min(8, Math.max(2, q.expr.split('\n').length))}
                        placeholder={
                          q.source === 'logs'
                            ? p.kind === 'logs'
                              ? '{service_name="api"} |= "timeout"'
                              : 'sum(count_over_time({service_name="api"} | detected_level="error" [$__interval]))'
                            : 'sum by (http_route) (rate(http_server_request_duration_seconds_count[$__interval]))'
                        }
                        onChange={(e) => setQuery(q.id, { expr: e.target.value, mode: 'code' })}
                      />
                    ))}
                </div>
              )
            })}
            {p.kind !== 'logs' && (
              <div className="dash-queries-foot">
                <button
                  className="m-add"
                  onClick={() => {
                    const q: Query = { id: uid(), name: letter(p.queries.length), source: 'metrics', mode: 'builder', expr: '' }
                    set({ queries: [...p.queries, q] })
                    setOpenQ(q.id)
                  }}
                >
                  <Plus size={12} />
                  Add query
                </button>
                <span className="t-caption t-note">$environment and other variables, $__interval and $__range work in any query.</span>
              </div>
            )}
          </section>
        </div>
        <aside className="panel dash-options">
          <div className="dash-opt">
            <div className="dash-opt-title">Show as</div>
            <div className="dash-kinds">
              {kinds.map(([k, label, icon]) => (
                <button key={k} className={'dash-kind' + (p.kind === k ? ' on' : '')} onClick={() => set({ kind: k, ...(k === 'logs' ? { queries: [{ ...p.queries[0], source: 'logs', mode: 'code' }] } : {}) })}>
                  <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
                    {icon}
                  </svg>
                  {label}
                </button>
              ))}
            </div>
          </div>
          <div className="dash-opt">
            <div className="dash-opt-title">Panel</div>
            <label className="dash-field">
              <span>Title</span>
              <input className="input" value={p.title} onChange={(e) => set({ title: e.target.value })} />
            </label>
            <div className="dash-field">
              <span>Width</span>
              <div className="seg small">
                {([1, 2, 4] as const).map((s) => (
                  <button key={s} className={p.span === s ? 'on' : ''} onClick={() => set({ span: s })}>
                    {s === 1 ? 'Quarter' : s === 2 ? 'Half' : 'Full'}
                  </button>
                ))}
              </div>
            </div>
          </div>
          {(p.kind === 'stat' || p.kind === 'bargauge') && (
            <div className="dash-opt">
              <div className="dash-opt-title">Value</div>
              <div className="seg small dash-wrap">
                {reduces.map(([k, l]) => (
                  <button key={k} className={p.reduce === k ? 'on' : ''} onClick={() => set({ reduce: k })}>
                    {l}
                  </button>
                ))}
              </div>
            </div>
          )}
          {(p.kind === 'timeseries' || p.kind === 'bars' || p.kind === 'table') && (
            <div className="dash-opt">
              <div className="dash-opt-title">Legend</div>
              {p.kind !== 'table' && (
                <div className="seg small">
                  {(['list', 'table', 'hidden'] as const).map((l) => (
                    <button key={l} className={p.legend === l ? 'on' : ''} onClick={() => set({ legend: l })}>
                      {l[0].toUpperCase() + l.slice(1)}
                    </button>
                  ))}
                </div>
              )}
              {(p.legend === 'table' || p.kind === 'table') && (
                <div className="dash-chips">
                  {(['mean', 'max', 'last', 'min'] as const).map((c) => (
                    <button key={c} className={'dash-chip' + (p.stats.includes(c) ? ' on' : '')} onClick={() => set({ stats: p.stats.includes(c) ? p.stats.filter((x) => x !== c) : [...p.stats, c] })}>
                      {c[0].toUpperCase() + c.slice(1)}
                    </button>
                  ))}
                </div>
              )}
            </div>
          )}
          {p.kind !== 'logs' && (
            <div className="dash-opt">
              <div className="dash-opt-title">Axis</div>
              <div className="dash-two">
                <label className="dash-field">
                  <span>Unit</span>
                  <select className="input" value={p.unit} onChange={(e) => set({ unit: e.target.value })}>
                    {units.map(([k, l]) => (
                      <option key={k} value={k}>
                        {l}
                      </option>
                    ))}
                  </select>
                </label>
                {(p.kind === 'timeseries' || p.kind === 'bars') && (
                  <label className="dash-field">
                    <span>Minimum</span>
                    <input className="input mono" inputMode="decimal" value={p.min ?? ''} placeholder="Auto" onChange={(e) => set({ min: e.target.value === '' || isNaN(+e.target.value) ? undefined : +e.target.value })} />
                  </label>
                )}
              </div>
            </div>
          )}
          {p.kind !== 'logs' && p.kind !== 'table' && p.kind !== 'bargauge' && p.kind !== 'heatmap' && p.kind !== 'histogram' && (
            <div className="dash-opt">
              <div className="dash-opt-title">Goal line</div>
              <div className="dash-goal">
                <span className="dash-goal-mark" />
                <input className="input mono" inputMode="decimal" value={p.goal ?? ''} placeholder="None" onChange={(e) => set({ goal: e.target.value === '' || isNaN(+e.target.value) ? undefined : +e.target.value })} />
                <span className="t-note">{units.find(([k]) => k === p.unit)?.[1].toLowerCase()}</span>
              </div>
            </div>
          )}
          {(p.kind === 'timeseries' || p.kind === 'bars') && (
            <div className="dash-opt">
              <div className="dash-opt-title">On the time axis</div>
              <div className="dash-toggle">
                <span>Deploys</span>
                <Switch on={p.deploys} onChange={(v) => set({ deploys: v })} label="Mark deploys" />
              </div>
            </div>
          )}
        </aside>
      </div>
    </>
  )
}

function rangeName(ms: number) {
  return ranges.find((r) => Math.abs(r.ms - ms) < 1000)?.label.toLowerCase() ?? 'this range'
}

function clock(ms: number, seconds = false) {
  return new Date(ms).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: seconds ? '2-digit' : undefined, hour12: false })
}

// message is a JSON line's msg or message, or the line itself.
function message(line: string) {
  if (!line.startsWith('{')) return line
  try {
    const v = JSON.parse(line)
    return typeof v?.msg === 'string' ? v.msg : typeof v?.message === 'string' ? v.message : line
  } catch {
    return line
  }
}

// bucketQuery counts a histogram's requests by bucket: over the whole range
// for a histogram panel, per step for a heatmap.
function bucketQuery(b: Built, kind: 'heatmap' | 'histogram') {
  const matchers = b.filters.map((x) => `${x.label}${x.op}${JSON.stringify(x.value)}`).join(', ')
  const agg = b.steps.find((s) => s.kind === 'agg') as { by: string[] } | undefined
  const by = ['le', ...(agg?.by ?? [])].join(', ')
  return `sum by (${by}) (increase(${b.metric}_bucket${matchers ? `{${matchers}}` : ''}[${kind === 'histogram' ? '$__range' : '$__interval'}]))`
}

// buckets reads series split by le as each bucket's own count at each
// moment, from Prometheus's running totals.
function buckets(series: Series[]) {
  const withLe = series.filter((s) => s.labels.le != null)
  const bounds = [...new Set(withLe.map((s) => s.labels.le))].map((le) => ({ le, at: le === '+Inf' ? Infinity : Number(le) })).sort((a, b) => a.at - b.at)
  const byLe = new Map<string, Series[]>()
  for (const s of withLe) byLe.set(s.labels.le, [...(byLe.get(s.labels.le) ?? []), s])
  const times = withLe[0]?.values.map(([t]) => t) ?? []
  // cumulative[bucket][time], summed over any other labels.
  const cumulative = bounds.map((b) => times.map((_, i) => (byLe.get(b.le) ?? []).reduce((a, s) => a + (s.values[i]?.[1] ?? 0), 0)))
  const counts = cumulative.map((row, bi) => row.map((v, ti) => Math.max(0, v - (bi > 0 ? cumulative[bi - 1][ti] : 0))))
  return { bounds: bounds.map((b) => b.at), times, cumulative, counts }
}

function quantile(q: number, bounds: number[], cum: number[]) {
  const total = cum[cum.length - 1] ?? 0
  if (!total) return null
  const rank = q * total
  for (let i = 0; i < bounds.length; i++) {
    if (cum[i] >= rank) {
      const lo = i ? bounds[i - 1] : 0
      if (!isFinite(bounds[i])) return lo
      const below = i ? cum[i - 1] : 0
      return lo + (bounds[i] - lo) * ((rank - below) / Math.max(1e-9, cum[i] - below))
    }
  }
  return bounds[bounds.length - 2] ?? null
}

const needsLe = <div className="dash-problem">This needs a query split by le, like sum by (le) (increase(request_duration_seconds_bucket[$__range]))</div>

function Histogram({ series, unit, height }: { series: Series[]; unit: string; height: number }) {
  const { bounds, cumulative, counts } = buckets(series)
  if (!bounds.length) return needsLe
  // The whole range: each bucket's count at the last moment.
  const last = (row: number[]) => [...row].reverse().find((v) => v != null) ?? 0
  const cum = cumulative.map(last)
  const own = counts.map(last)
  const total = cum[cum.length - 1] ?? 0
  const shown = own.map((v, i) => ({ v, at: bounds[i] })).filter((b, i, all) => isFinite(b.at) || b.v > 0 || i < all.length - 1)
  const max = Math.max(1, ...shown.map((b) => b.v))
  const marks = [0.5, 0.95, 0.99].map((q) => ({ q, v: quantile(q, bounds, cum) }))
  const every = Math.ceil(shown.length / 8)
  // Where a value sits along the bars: its bucket, and how far into it.
  const along = (v: number) => {
    const i = shown.findIndex((b) => v <= b.at)
    if (i < 0) return 100
    const lo = i ? shown[i - 1].at : 0
    const f = isFinite(shown[i].at) ? (v - lo) / Math.max(1e-9, shown[i].at - lo) : 0.5
    return ((i + Math.min(1, Math.max(0, f))) / shown.length) * 100
  }
  return (
    <div className="dash-hist">
      <div className="t-caption t-muted">{Math.round(total).toLocaleString()} in this range, by how long each took</div>
      <div className="dash-hist-plot" style={{ height }}>
        {marks.map((m, i) => {
          if (m.v == null) return null
          // A mark close to the one before goes a line higher.
          const near = i > 0 && marks[i - 1].v != null && Math.abs(along(m.v) - along(marks[i - 1].v!)) < 14
          return (
            <span key={m.q} className="dash-hist-mark" style={{ left: `${along(m.v)}%`, top: near ? -36 : -20 }}>
              <span className="dash-hist-label">
                <b>p{Math.round(m.q * 100)}</b> {bound(m.v, unit)}
              </span>
            </span>
          )
        })}
        {shown.map((b, i) => (
          <span key={i} className="dash-hist-bar" style={{ height: `${(b.v / max) * 100}%` }} title={`${i ? format(shown[i - 1].at, unit) : '0'} to ${isFinite(b.at) ? format(b.at, unit) : 'more'}: ${Math.round(b.v).toLocaleString()}`} />
        ))}
      </div>
      <div className="dash-hist-x mono">
        {shown.map((b, i) => (
          <span key={i}>{i % every === every - 1 || i === shown.length - 1 ? (isFinite(b.at) ? bound(b.at, unit) : '') : ''}</span>
        ))}
      </div>
    </div>
  )
}

function Heatmap({ series, unit, height }: { series: Series[]; unit: string; height: number }) {
  const [hover, setHover] = useState<{ r: number; c: number } | null>(null)
  const { bounds, times, counts } = buckets(series)
  if (!bounds.length || !times.length) return needsLe
  // At most 40 columns: neighbouring steps add up.
  const per = Math.ceil(times.length / 40)
  const cols = Math.ceil(times.length / per)
  const grid = counts.map((row) => Array.from({ length: cols }, (_, c) => row.slice(c * per, c * per + per).reduce((a, v) => a + v, 0)))
  // Drop the top bucket when nothing went past the last bound.
  const rows = grid.length > 1 && grid[grid.length - 1].every((v) => v === 0) ? grid.slice(0, -1) : grid
  const max = Math.max(1, ...rows.flat())
  const color = (v: number) => {
    if (!v) return 'rgba(255, 255, 255, 0.03)'
    const t = Math.sqrt(v / max)
    const mix = (a: number[], b: number[], f: number) => a.map((x, i) => Math.round(x + (b[i] - x) * f))
    const c = t < 0.6 ? mix([20, 40, 72], [57, 135, 229], t / 0.6) : mix([57, 135, 229], [168, 201, 248], (t - 0.6) / 0.4)
    return `rgb(${c.join(',')})`
  }
  const label = (i: number) => (isFinite(bounds[i]) ? bound(bounds[i], unit) : `over ${bound(bounds[i - 1], unit)}`)
  const cellH = Math.max(10, Math.min(24, (height - 10) / rows.length))
  const h = hover ? { from: times[hover.c * per], to: times[Math.min(times.length - 1, hover.c * per + per)], v: rows[hover.r][hover.c] } : null
  return (
    <div className="dash-heat">
      <div className="dash-heat-scale mono">
        <span>0</span>
        <span className="dash-heat-ramp" />
        <span>{Math.round(max).toLocaleString()}</span>
      </div>
      <div className="dash-heat-body">
        <div className="dash-heat-y mono">
          {[...rows.keys()].reverse().map((r) => (
            <span key={r} style={{ height: cellH }}>
              {r % Math.ceil(rows.length / 8) === 0 ? label(r) : ''}
            </span>
          ))}
        </div>
        <div className="dash-heat-grid" style={{ gridTemplateColumns: `repeat(${cols}, 1fr)` }} onMouseLeave={() => setHover(null)}>
          {[...rows.keys()].reverse().flatMap((r) =>
            rows[r].map((v, c) => <span key={`${r}-${c}`} className={hover && hover.r === r && hover.c === c ? 'on' : ''} style={{ height: cellH, background: color(v) }} onMouseEnter={() => setHover({ r, c })} />),
          )}
          {h && hover && (
            <div className={'a-tip' + (hover.c > cols * 0.6 ? ' left' : '')} style={{ left: `${((hover.c + 1) / cols) * 100}%`, top: 0 }}>
              <div className="a-tip-head">
                {clock(h.from)} to {clock(h.to)}
              </div>
              <div>
                Range<b>{hover.r ? bound(bounds[hover.r - 1], unit) : '0'} to {label(hover.r)}</b>
              </div>
              <div>
                Count<b>{Math.round(h.v).toLocaleString()}</b>
              </div>
            </div>
          )}
        </div>
      </div>
      <div className="a-x dash-heat-x">
        {[0, 0.5, 1].map((f) => (
          <span key={f}>{clock(times[Math.min(times.length - 1, Math.round(f * (times.length - 1)))])}</span>
        ))}
      </div>
    </div>
  )
}

// States draws each series as a band over time: up, slow (over the goal)
// or down (zero), merged into runs.
function States({ series, names, from, to, goal, unit }: { series: Series[]; names: string[]; from: number; to: number; goal?: number; unit: string }) {
  const stateOf = (v: number | null) => (v == null ? null : v <= 0 ? 'down' : goal != null && v > goal ? 'slow' : 'up')
  const colors: Record<string, string> = { up: '#3d7f76', slow: 'var(--d-amber)', down: 'var(--d-red)' }
  const rows = series.slice(0, 12).map((s, i) => {
    const runs: { state: string; from: number; to: number }[] = []
    const step = s.values.length > 1 ? s.values[1][0] - s.values[0][0] : 60_000
    for (const [t, v] of s.values) {
      const st = stateOf(v)
      if (!st) continue
      const lastRun = runs[runs.length - 1]
      if (lastRun && lastRun.state === st && t - lastRun.to <= step * 1.5) lastRun.to = t + step
      else runs.push({ state: st, from: t, to: t + step })
    }
    return { name: names[i], runs }
  })
  // The longest time down, or else slow, for the line under the title.
  const longest = (state: string) => rows.flatMap((r) => r.runs.filter((x) => x.state === state).map((x) => ({ name: r.name, ...x }))).sort((a, b) => b.to - b.from - (a.to - a.from))[0]
  const worst = longest('down')
  const slow = longest('slow')
  const x = (t: number) => `${Math.max(0, Math.min(100, ((t - from) / (to - from)) * 100))}%`
  return (
    <div className="dash-states">
      <div className="t-caption t-muted">
        {worst
          ? `${worst.name} was down ${dur(worst.to - worst.from)} at ${clock(worst.from)}`
          : slow && goal != null
            ? `${slow.name} was over ${bound(goal, unit)} for ${dur(slow.to - slow.from)} at ${clock(slow.from)}`
            : `Up the whole range${goal != null ? `, ${bound(goal, unit)} or under` : ''}`}
      </div>
      <div className="a-legend">
        {(['up', ...(goal != null ? ['slow'] : []), 'down'] as const).map((k) => (
          <span key={k}>
            <span className="a-dot" style={{ background: colors[k] }} />
            {k === 'up' ? 'Up' : k === 'slow' ? 'Slow' : 'Down'}
          </span>
        ))}
      </div>
      {rows.map((r) => (
        <div key={r.name} className="dash-state-row">
          <span className="mono">{r.name}</span>
          <span className="dash-state-band">
            {r.runs.map((run, i) => (
              <span key={i} style={{ left: x(run.from), width: `calc(${x(run.to)} - ${x(run.from)})`, background: colors[run.state] }} title={`${run.state} from ${clock(run.from)} to ${clock(run.to)}`} />
            ))}
          </span>
        </div>
      ))}
      <div className="a-x dash-states-x">
        {[0, 0.5, 1].map((f) => (
          <span key={f}>{clock(from + f * (to - from))}</span>
        ))}
      </div>
    </div>
  )
}

function dur(ms: number) {
  const n = (v: number, one: string) => `${v} ${one}${v === 1 ? '' : 's'}`
  if (ms < 60_000) return n(Math.round(ms / 1000), 'second')
  if (ms < 3_600_000) return n(Math.round(ms / 60_000), 'minute')
  return n(+(ms / 3_600_000).toFixed(1), 'hour')
}

// bound names a bucket's edge shortly: 250 ms, 1 s, 10 s.
function bound(v: number, unit: string) {
  const ms = unit === 'seconds' ? v * 1000 : unit === 'milliseconds' ? v : unit === 'microseconds' ? v / 1000 : unit === 'nanoseconds' ? v / 1e6 : null
  if (ms == null) return format(v, unit)
  if (ms < 1000) return `${+ms.toPrecision(3)} ms`
  if (ms < 60_000) return `${+(ms / 1000).toPrecision(3)} s`
  return `${+(ms / 60_000).toPrecision(3)} min`
}
