import { useEffect, useState, type ReactNode } from 'react'
import type { AnalyticsList, AnalyticsOverview, Deployment } from '../lib/api'
import { useData } from '../lib/data'
import { Sparkline, TrafficChart, type Deploy } from '../components/AnalyticsCharts'
import { Calendar, Disclosure } from '../components/Icons'
import { Menu } from '../components/Menu'
import { Problem, Skeleton } from '../components/ui'
import { LogExplorer } from './LogExplorer'

// Analytics is the project's traffic and logs. Overview counts visitors,
// pages and errors from the request log Pier already keeps; Logs is every
// request, line by line.

type View = 'overview' | 'logs'
const views: [View, string][] = [
  ['overview', 'Overview'],
  ['logs', 'Logs'],
]

export function Analytics({ project, initial }: { project: string; initial: View }) {
  const [view, setView] = useState<View>(initial)
  useEffect(() => setView(initial), [initial])
  const pick = (v: View) => {
    setView(v)
    history.replaceState(null, '', `/projects/${encodeURIComponent(project)}/analytics${v === 'overview' ? '' : '?view=' + v}`)
  }
  const tabs = (right?: ReactNode) => (
    <div className="a-head">
      <div className="seg" role="tablist" aria-label="Analytics">
        {views.map(([v, label]) => (
          <button key={v} role="tab" aria-selected={view === v} className={view === v ? 'on' : ''} onClick={() => pick(v)}>
            {label}
          </button>
        ))}
      </div>
      {right && <div className="a-head-right">{right}</div>}
    </div>
  )
  if (view === 'logs') return <LogExplorer project={project} head={tabs()} />
  return <Overview project={project} tabs={tabs} />
}

const ranges = [
  { key: '1h', label: 'Last hour', ms: 3_600_000 },
  { key: '24h', label: 'Last 24 hours', ms: 86_400_000 },
  { key: '7d', label: 'Last 7 days', ms: 7 * 86_400_000 },
  { key: '14d', label: 'Last 14 days', ms: 14 * 86_400_000 },
]
const envs = [
  { key: 'production', label: 'Production' },
  { key: 'preview', label: 'Previews' },
  { key: '', label: 'All environments' },
]

function Overview({ project, tabs }: { project: string; tabs: (right: ReactNode) => ReactNode }) {
  const [range, setRange] = useState(ranges[1])
  const [env, setEnv] = useState(envs[0])
  const [to, setTo] = useState(() => Date.now())
  useEffect(() => {
    const t = window.setInterval(() => setTo(Date.now()), 30_000)
    return () => window.clearInterval(t)
  }, [])
  const from = to - range.ms
  const minute = (t: number) => t - (t % 60_000)
  const q = new URLSearchParams({ from: String(minute(from)), to: String(minute(to) + 60_000) })
  if (env.key) q.set('env', env.key)
  const o = useData<AnalyticsOverview>(`/api/projects/${encodeURIComponent(project)}/analytics?${q}`)
  const deps = useData<{ deployments: Deployment[] }>(`/api/projects/${encodeURIComponent(project)}/deployments`)
  const deploys: Deploy[] = (deps.data?.deployments ?? [])
    .filter((d) => d.target === 'production' && d.status === 'ready' && (env.key === '' || env.key === 'production'))
    .map((d) => ({ id: d.id, at: Date.parse(d.created_at) + (d.build_ms || 0) }))

  const right = (
    <>
      <Menu label="Environment" align="end" className="btn quiet medium" items={envs.map((e) => ({ label: e.label, onSelect: () => setEnv(e) }))}>
        {env.key === 'production' && <span className="a-dot" style={{ background: 'var(--d-pink)' }} />}
        {env.key === 'preview' && <span className="a-dot" style={{ background: 'var(--d-violet)' }} />}
        {env.label}
        <span className="a-chev">
          <Disclosure />
        </span>
      </Menu>
      <Menu label="Time range" align="end" className="btn quiet medium" items={ranges.map((r) => ({ label: r.label, onSelect: () => setRange(r) }))}>
        <Calendar />
        {range.label}
      </Menu>
    </>
  )
  if (o.error) {
    return (
      <main className="main" style={{ gap: 28 }}>
        {tabs(right)}
        <Problem>{o.error.message}</Problem>
      </main>
    )
  }
  const d = o.data
  return (
    <main className="main" style={{ gap: 28 }}>
      {tabs(right)}
      {!d ? (
        <div className="panel">
          <Skeleton rows={4} tall />
        </div>
      ) : d.requests === 0 ? (
        <div className="panel a-empty">
          <div className="t-small">No requests in this range</div>
          <div className="t-caption t-muted">Visits show here as soon as someone opens a deployment.</div>
        </div>
      ) : (
        <>
          <Headline d={d} />
          <Section title="Requests" right={<Legend items={[['Requests', 'var(--d-blue)', d.requests], ['Errors', 'var(--d-red)', d.errors]]} />}>
            <div className="panel a-chart-panel">
              <TrafficChart points={d.trend} from={d.from} to={d.to} deploys={deploys} />
            </div>
          </Section>
          <div className="a-grid2">
            <Breakdown title="Pages" label="Page" measure="Views" list={d.pages} rest={(n, k) => `${n.toLocaleString()} more across ${k} pages`} mono />
            <Breakdown title="Referrers" label="Site" measure="Views" list={d.referrers} rest={(n, k) => `${n.toLocaleString()} more from ${k} sites`} mono name={(n) => n || 'Direct'} />
          </div>
          <div className="a-grid2">
            {d.countries.top.length > 0 ? (
              <Breakdown title="Countries" label="Country" measure="Visitors" list={d.countries} rest={(n, k) => `${n.toLocaleString()} more from ${k} countries`} name={countryName} />
            ) : (
              <Breakdown title="Devices" label="Device" measure="Visitors" list={d.devices} />
            )}
            <Breakdown title="Browsers" label="Browser" measure="Visitors" list={d.browsers} rest={(n, k) => `${n.toLocaleString()} more on ${k} others`} />
          </div>
          <div className="a-grid2">
            <Slowest d={d} />
            <Breakdown title="Bots" label="Bot" measure="Requests" list={d.bots} rest={(n, k) => `${n.toLocaleString()} more from ${k} others`}
              foot={d.bots.top.length ? `Left out of visitors, page views and response times` : undefined} />
          </div>
        </>
      )}
    </main>
  )
}

function Headline({ d }: { d: AnalyticsOverview }) {
  const errorRate = d.requests ? (d.errors / d.requests) * 100 : 0
  const change = (now: number, before: number) => {
    if (!before) return 'Nothing the range before'
    const p = Math.round(((now - before) / before) * 100)
    return p === 0 ? 'Same as the range before' : `${Math.abs(p)}% ${p > 0 ? 'more' : 'fewer'} than the range before`
  }
  const cells: { label: string; value: string; note: ReactNode; values: (number | null)[] }[] = [
    { label: 'Visitors', value: d.visitors.toLocaleString(), note: change(d.visitors, d.before.visitors), values: d.trend.map((p) => p.visitors) },
    { label: 'Page views', value: d.page_views.toLocaleString(), note: d.visitors ? `${(d.page_views / d.visitors).toFixed(1)} per visitor` : '', values: d.trend.map((p) => p.page_views) },
    {
      label: 'Error rate',
      value: errorRate === 0 ? '0%' : `${errorRate < 0.1 ? errorRate.toFixed(2) : errorRate.toFixed(1)}%`,
      note: <State ok={errorRate < 1}>{errorRate < 1 ? 'Under 1% of requests' : 'Over 1% of requests'}</State>,
      values: d.trend.map((p) => (p.requests ? p.errors / p.requests : 0)),
    },
    { label: 'Response time, p95', value: ms(d.p95_ms), note: d.before.p95_ms ? `${ms(d.before.p95_ms)} the range before` : 'Slowest 5% of requests', values: d.trend.map((p) => p.p95_ms) },
  ]
  return (
    <div className="panel a-strip">
      {cells.map((c) => (
        <div key={c.label} className="a-cell">
          <div className="a-label">{c.label}</div>
          <div className="a-value">{c.value}</div>
          <div className="a-note">{c.note}</div>
          <Sparkline values={c.values} />
        </div>
      ))}
    </div>
  )
}

function State({ ok, children }: { ok: boolean; children: ReactNode }) {
  return (
    <span className="a-state">
      <span className="a-dot" style={{ background: ok ? 'var(--d-teal)' : 'var(--d-red)' }} />
      {children}
    </span>
  )
}

function Section({ title, right, note, children }: { title: string; right?: ReactNode; note?: string; children: ReactNode }) {
  return (
    <section className="a-section">
      <div className="a-section-head">
        <h2>
          {title}
          {note && <span>{note}</span>}
        </h2>
        {right}
      </div>
      {children}
    </section>
  )
}

function Legend({ items }: { items: [string, string, number][] }) {
  return (
    <div className="a-legend">
      {items.map(([n, c, v]) => (
        <span key={n}>
          <span className="a-dot" style={{ background: c }} />
          {n}
          <b>{v.toLocaleString()}</b>
        </span>
      ))}
    </div>
  )
}

function Breakdown({ title, label, measure, list, rest, mono, name = (n) => n, foot }: {
  title: string
  label: string
  measure: string
  list: AnalyticsList
  rest?: (n: number, k: number) => string
  mono?: boolean
  name?: (n: string) => string
  foot?: string
}) {
  const max = Math.max(1, ...list.top.map((c) => c.count))
  const tail = list.others > 0 && rest ? rest(list.rest, list.others) : foot
  return (
    <Section title={title}>
      <div className="table a-breakdown" style={{ '--cols': '1.6fr 0.6fr 0.9fr' } as React.CSSProperties}>
        <div className="t-head">
          <span>{label}</span>
          <span className="t-right">{measure}</span>
          <span />
        </div>
        {list.top.length === 0 ? (
          <div className="a-none">None in this range</div>
        ) : (
          list.top.map((c) => (
            <div key={c.name} className="t-row">
              <span className={mono && c.name ? 'mono a-name' : 'a-name'}>{name(c.name)}</span>
              <span className="t-right mono t-muted">{c.count.toLocaleString()}</span>
              <span className="a-bar">
                <span style={{ width: `${(c.count / max) * 100}%` }} />
              </span>
            </div>
          ))
        )}
        {tail && <div className="a-foot">{tail}</div>}
      </div>
    </Section>
  )
}

function Slowest({ d }: { d: AnalyticsOverview }) {
  return (
    <Section title="Slowest routes">
      <div className="table a-breakdown" style={{ '--cols': '1.7fr 0.7fr 0.7fr' } as React.CSSProperties}>
        <div className="t-head">
          <span>Route</span>
          <span className="t-right">p95</span>
          <span className="t-right">Requests</span>
        </div>
        {d.slowest.length === 0 ? (
          <div className="a-none">No route had five requests yet</div>
        ) : (
          d.slowest.map((r) => (
            <div key={r.route} className="t-row">
              <span className="mono a-name">{r.route}</span>
              <span className="t-right mono">{ms(r.p95_ms)}</span>
              <span className="t-right mono t-muted">{r.requests.toLocaleString()}</span>
            </div>
          ))
        )}
        <div className="a-foot">The slowest 5% of each route's requests took this long or longer</div>
      </div>
    </Section>
  )
}

export function ms(v: number) {
  if (v > 0 && v < 1) return '<1 ms'
  if (v >= 1000) return `${(v / 1000).toFixed(v >= 10_000 ? 0 : 2)} s`
  return `${Math.round(v)} ms`
}

const regions = typeof Intl !== 'undefined' && 'DisplayNames' in Intl ? new Intl.DisplayNames(undefined, { type: 'region' }) : null
function countryName(code: string) {
  try {
    return regions?.of(code) ?? code
  } catch {
    return code
  }
}
