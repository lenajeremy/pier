import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { api } from '../lib/api'
import { Calendar, Close, Copy, Disclosure, Search, Visit } from '../components/Icons'
import { explore, useGrafana } from '../lib/grafana'
import { Menu } from '../components/Menu'
import { toast } from '../components/Toast'
import { Skeleton } from '../components/ui'
import { serviceColor } from './Traces'

// Logs, once traces, metrics and app logs are on: every request Pier's
// router answered and everything the app printed or sent, from Loki, as
// one stream. Fields come from Loki's own detection, so whatever an app
// logs as JSON can be filtered on.

type Field = { name: string; type: string; cardinality: number; parsers?: string[]; label?: boolean }
type Entry = { at: number; ns: string; labels: Record<string, string>; line: string }
type Bucket = { at: number; levels: Record<string, number> }
type Filter = { field: string; value: string; not?: boolean }

const ranges = [
  { key: '15m', label: 'Last 15 minutes', ms: 15 * 60_000 },
  { key: '1h', label: 'Last hour', ms: 3_600_000 },
  { key: '6h', label: 'Last 6 hours', ms: 6 * 3_600_000 },
  { key: '24h', label: 'Last 24 hours', ms: 86_400_000 },
  { key: '7d', label: 'Last 7 days', ms: 7 * 86_400_000 },
]

// The sidebar's groups, by the names Loki gives fields.
const groups: [string, string[]][] = [
  ['Request', ['http_request_method', 'url_path', 'http_response_status_code', 'server_address', 'duration_ms', 'url_query', 'http_response_body_size']],
  ['Client', ['browser', 'device', 'country', 'referrer', 'bot']],
  ['Service', ['service_name', 'deployment_environment', 'pier_deployment', 'service_instance_id', 'scope_name']],
  ['Trace', ['trace_id', 'span_id']],
  ['Log', ['detected_level', 'level', 'severity_text', 'log_iostream', 'msg', 'message']],
]
const hidden = new Set(['severity_number', 'observed_timestamp', 'pier_project', 'service_namespace'])
const names: Record<string, string> = {
  http_request_method: 'method',
  url_path: 'path',
  http_response_status_code: 'status',
  server_address: 'host',
  http_response_body_size: 'size',
  url_query: 'query',
  service_name: 'service',
  deployment_environment: 'environment',
  pier_deployment: 'deployment',
  service_instance_id: 'instance',
  scope_name: 'scope',
  detected_level: 'level',
  log_iostream: 'stream',
}
const nameOf = (f: string) => names[f] ?? f

const levels: [string, string, string][] = [
  ['error', 'Error', 'var(--d-red)'],
  ['warn', 'Warning', 'var(--d-amber)'],
  ['info', 'Info', 'var(--d-blue)'],
]
function levelOf(l?: string): 'error' | 'warn' | 'info' | 'other' {
  if (!l) return 'other'
  if (/^(error|err|fatal|critical|crit|emerg|alert|panic)/i.test(l)) return 'error'
  if (/^warn/i.test(l)) return 'warn'
  if (/^(info|notice)/i.test(l)) return 'info'
  return 'other'
}

const quote = (v: string) => JSON.stringify(v)
const regexEscape = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')

// logql writes the query: a stream selector, then the filters, then a
// search. Text that starts with { is a query of its own.
function logql(fields: Field[], filters: Filter[], text: string) {
  const byName = new Map(fields.map((f) => [f.name, f]))
  const raw = text.trim().startsWith('{')
  const matchers = raw ? [] : ['service_name=~".+"']
  const pipe: string[] = []
  let parsed = ''
  for (const f of filters) {
    const field = byName.get(f.field)
    const op = f.not ? '!=' : '='
    if (field?.label && !raw) {
      matchers.push(`${f.field}${op}${quote(f.value)}`)
      continue
    }
    const parser = field?.parsers?.[0]
    if (parser && !parsed.includes(parser)) {
      pipe.push(`| ${parser}`)
      parsed += parser
    }
    pipe.push(`| ${f.field}${op}${quote(f.value)}`)
  }
  if (parsed) pipe.push('| drop __error__, __error_details__')
  if (!raw && text.trim()) pipe.unshift(`|~ ${quote('(?i)' + regexEscape(text.trim()))}`)
  const head = raw ? text.trim() : `{${matchers.join(', ')}}`
  return [head, ...pipe].join(' ')
}

export function LokiLogs({ project, head, onTrace }: { project: string; head: ReactNode; onTrace: (id: string) => void }) {
  const base = `/api/projects/${encodeURIComponent(project)}/logs`
  const [range, setRange] = useState(ranges[1])
  const [zoom, setZoom] = useState<{ from: number; to: number } | null>(null)
  const [filters, setFilters] = useState<Filter[]>(() => (new URLSearchParams(location.search).get('status') === 'errors' ? [{ field: 'detected_level', value: 'error' }] : []))
  const [typed, setTyped] = useState('')
  const [text, setText] = useState('')
  const [now, setNow] = useState(() => Date.now())
  const [fields, setFields] = useState<Field[]>([])
  const [volume, setVolume] = useState<{ buckets: Bucket[]; step: number } | null>(null)
  const [lines, setLines] = useState<Entry[] | null>(null)
  const [more, setMore] = useState(false)
  const [problem, setProblem] = useState('')
  const [open, setOpen] = useState<string | null>(null)
  const [field, setField] = useState<string | null>(null)
  const [fieldSearch, setFieldSearch] = useState('')

  useEffect(() => {
    const t = window.setTimeout(() => setText(typed), 400)
    return () => window.clearTimeout(t)
  }, [typed])
  useEffect(() => {
    if (zoom) return
    const t = window.setInterval(() => setNow(Date.now()), 15_000)
    return () => window.clearInterval(t)
  }, [zoom])

  const query = logql(fields, filters, text)
  const grafana = useGrafana(project)
  const bounds = zoom ?? { from: now - range.ms, to: now }
  const params = (extra: Record<string, string> = {}) => new URLSearchParams({ q: query, from: String(Math.floor(bounds.from)), to: String(Math.ceil(bounds.to)), ...extra })

  useEffect(() => {
    let stopped = false
    setProblem('')
    Promise.allSettled([api<{ lines: Entry[]; limit: number }>(`${base}/lines?${params({ limit: '200' })}`), api<{ buckets: Bucket[]; step: number }>(`${base}/volume?${params()}`)]).then(([l, v]) => {
      if (stopped) return
      // The lines' error first: the volume wraps the query, so its message points at the wrong place.
      const failed = l.status === 'rejected' ? l.reason : v.status === 'rejected' ? v.reason : null
      if (failed || l.status !== 'fulfilled' || v.status !== 'fulfilled') {
        setProblem(failed instanceof Error ? failed.message : 'Loki didn’t answer')
        setLines([])
        setVolume(null)
        return
      }
      setLines(l.value.lines)
      setMore(l.value.lines.length >= l.value.limit)
      setVolume(v.value)
    })
    return () => {
      stopped = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query, bounds.from, bounds.to])
  // Fields for the range, whatever is filtered, so a filter can be undone from the list.
  useEffect(() => {
    let stopped = false
    const p = new URLSearchParams({ from: String(Math.floor(bounds.from)), to: String(Math.ceil(bounds.to)) })
    api<{ fields: Field[] }>(`${base}/fields?${p}`).then((d) => !stopped && setFields(d.fields), () => {})
    return () => {
      stopped = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [range, zoom])

  const older = async () => {
    if (!lines?.length) return
    try {
      const l = await api<{ lines: Entry[]; limit: number }>(`${base}/lines?${params({ limit: '200', before: lines[lines.length - 1].ns })}`)
      setLines([...lines, ...l.lines])
      setMore(l.lines.length >= l.limit)
    } catch (e) {
      toast(e instanceof Error ? e.message : 'Couldn’t load more', { tone: 'failed' })
    }
  }
  const add = (f: Filter) => setFilters((fs) => [...fs.filter((x) => !(x.field === f.field && x.value === f.value)), f])

  const totals = useMemo(() => {
    const t: Record<string, number> = { error: 0, warn: 0, info: 0, other: 0 }
    for (const b of volume?.buckets ?? []) for (const [l, n] of Object.entries(b.levels)) t[levelOf(l)] += n
    return t
  }, [volume])
  const count = Object.values(totals).reduce((a, b) => a + b, 0)

  const visible = fields.filter((f) => !hidden.has(f.name))
  const known = new Set(groups.flatMap(([, fs]) => fs))
  const grouped: [string, Field[]][] = [
    ...groups.map(([g, fs]) => [g, fs.map((n) => visible.find((f) => f.name === n)).filter((f): f is Field => !!f)] as [string, Field[]]),
    ['Other fields', visible.filter((f) => !known.has(f.name)).sort((a, b) => a.name.localeCompare(b.name))],
  ]
  const find = fieldSearch.trim().toLowerCase()
  const shown = grouped.map(([g, fs]) => [g, fs.filter((f) => !find || f.name.includes(find) || nameOf(f.name).includes(find))] as [string, Field[]]).filter(([, fs]) => fs.length)

  return (
    <main className="main" style={{ gap: 16 }}>
      {head}
      <div className="le">
        <aside className="panel le-fields">
          <label className="le-input">
            <Search />
            <input value={fieldSearch} onChange={(e) => setFieldSearch(e.target.value)} placeholder="Search fields" aria-label="Search fields" />
          </label>
          {fields.length === 0 && <div className="le-note">No fields in this range</div>}
          {shown.map(([g, fs]) => (
            <div key={g} className="le-group">
              <div className="le-group-name">{g}</div>
              {fs.map((f) => (
                <div key={f.name}>
                  <button className={'le-field' + (field === f.name ? ' on' : '')} onClick={() => setField(field === f.name ? null : f.name)} aria-expanded={field === f.name}>
                    <span className="mono">{nameOf(f.name)}</span>
                    <span className={'le-chev' + (field === f.name ? ' down' : '')}>
                      <Disclosure />
                    </span>
                  </button>
                  {field === f.name && <Values base={base} params={params({ field: f.name, parsed: f.parsers?.[0] ?? '' })} onPick={(v) => add({ field: f.name, value: v })} />}
                </div>
              ))}
            </div>
          ))}
        </aside>
        <div className="le-main">
          <div className="le-toolbar">
            <Menu label="Add a filter" align="start" className="btn quiet medium" items={visible.slice(0, 40).map((f) => ({ label: nameOf(f.name), hint: f.name !== nameOf(f.name) ? f.name : undefined, onSelect: () => setField(f.name) }))}>
              Add filter
            </Menu>
            <label className="le-input grow">
              <Search />
              <input className={typed.trim().startsWith('{') ? 'mono' : ''} value={typed} onChange={(e) => setTyped(e.target.value)} placeholder='Search, or write a query like {service_name="api"} |= "timeout"' aria-label="Search, or write a LogQL query" spellCheck={false} />
            </label>
            <Menu label="Time range" align="end" className="btn quiet medium" items={ranges.map((r) => ({ label: r.label, onSelect: () => (setZoom(null), setRange(r), setNow(Date.now())) }))}>
              <Calendar />
              {range.label}
            </Menu>
            {grafana && (
              <a className="btn quiet medium" href={explore(grafana, 'logs', { expr: query, queryType: 'range' }, bounds.from, bounds.to)} target="_blank" rel="noreferrer" title="Open in Grafana">
                Grafana
                <Visit size={13} />
              </a>
            )}
          </div>
          {(filters.length > 0 || zoom || count > 0) && (
            <div className="le-chips">
              {filters.map((f, i) => (
                <span key={`${f.field}${f.value}${f.not}`} className="le-chip">
                  <button className="le-chip-op" onClick={() => setFilters((fs) => fs.map((x, j) => (j === i ? { ...x, not: !x.not } : x)))} title="Switch between is and isn't">
                    {nameOf(f.field)} {f.not ? 'is not' : 'is'}
                  </button>
                  <b className="mono">{f.value || 'empty'}</b>
                  <button aria-label="Remove this filter" onClick={() => setFilters((fs) => fs.filter((_, j) => j !== i))}>
                    <Close />
                  </button>
                </span>
              ))}
              {zoom && (
                <span className="le-chip on">
                  time is <b className="mono">{clock(zoom.from)} to {clock(zoom.to)}</b>
                  <button aria-label="Show the whole range" onClick={() => (setZoom(null), setNow(Date.now()))}>
                    <Close />
                  </button>
                </span>
              )}
              {count > 0 && <span className="le-count">{count.toLocaleString()} lines</span>}
            </div>
          )}
          {problem ? (
            <div className="panel m-error mono">{problem}</div>
          ) : !lines || !volume ? (
            <div className="panel">
              <Skeleton rows={5} />
            </div>
          ) : (
            <>
              <div className="panel le-chart">
                <div className="le-chart-head">
                  <span>Lines</span>
                  <div className="a-legend">
                    {levels.map(([k, label, color]) => (
                      <span key={k}>
                        <span className="a-dot" style={{ background: color }} />
                        {label}
                        <b>{totals[k].toLocaleString()}</b>
                      </span>
                    ))}
                  </div>
                </div>
                <Volume buckets={volume.buckets} step={volume.step} onPick={(b) => setZoom({ from: b.at, to: b.at + volume.step })} />
              </div>
              {lines.length === 0 ? (
                <div className="panel a-empty">
                  <div className="t-small">No lines match</div>
                  <div className="t-caption t-muted">Try a wider range, or fewer filters.</div>
                </div>
              ) : (
                <div className="table le-lines lg-lines" style={{ '--cols': '14px 112px 84px 80px minmax(0, 1fr) 84px' } as React.CSSProperties}>
                  <div className="t-head">
                    <span />
                    <span>Time</span>
                    <span>Level</span>
                    <span>Service</span>
                    <span>Message</span>
                    <span>Trace</span>
                  </div>
                  {lines.map((e) => (
                    <Line key={e.ns + e.line.slice(0, 40)} e={e} open={open === e.ns + e.line} setOpen={(o) => setOpen(o ? e.ns + e.line : null)} onFilter={add} onTrace={onTrace} />
                  ))}
                  {more && (
                    <button className="tr-more" onClick={older}>
                      Show older lines
                    </button>
                  )}
                </div>
              )}
            </>
          )}
        </div>
      </div>
    </main>
  )
}

function Values({ base, params, onPick }: { base: string; params: URLSearchParams; onPick: (v: string) => void }) {
  const [data, setData] = useState<{ value: string; count: number }[] | null>(null)
  const [failed, setFailed] = useState(false)
  const key = params.toString()
  useEffect(() => {
    let stopped = false
    setData(null)
    setFailed(false)
    api<{ values: { value: string; count: number }[] }>(`${base}/values?${params}`).then(
      (d) => !stopped && setData(d.values),
      () => !stopped && setFailed(true),
    )
    return () => {
      stopped = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key])
  if (failed) return <div className="le-values le-note">Couldn’t count its values</div>
  if (!data) return <div className="le-values le-note">Counting</div>
  if (!data.length) return <div className="le-values le-note">None in this range</div>
  const max = Math.max(...data.map((v) => v.count))
  return (
    <div className="le-values">
      {data.map((v) => (
        <button key={v.value} className="le-value" onClick={() => onPick(v.value)} title={`Only ${v.value}`}>
          <span className="le-value-row">
            <span className="mono">{v.value}</span>
            <span className="mono t-muted">{v.count.toLocaleString()}</span>
          </span>
          <span className="a-bar">
            <span style={{ width: `${(v.count / max) * 100}%` }} />
          </span>
        </button>
      ))}
    </div>
  )
}

function Volume({ buckets, step, onPick }: { buckets: Bucket[]; step: number; onPick: (b: Bucket) => void }) {
  const sums = buckets.map((b) => {
    const t: Record<string, number> = { error: 0, warn: 0, info: 0, other: 0 }
    for (const [l, n] of Object.entries(b.levels)) t[levelOf(l)] += n
    return t
  })
  const max = Math.max(1, ...sums.map((t) => t.error + t.warn + t.info + t.other))
  const span = buckets.length ? buckets[buckets.length - 1].at + step - buckets[0].at : 0
  const day = span > 26 * 3600_000
  const ticks = buckets.length ? [0, 0.25, 0.5, 0.75, 1].map((f) => buckets[0].at + f * span) : []
  return (
    <div className="le-bars-wrap">
      <div className="le-bars">
        {buckets.map((b, i) => {
          const t = sums[i]
          const n = t.error + t.warn + t.info + t.other
          return (
            <button key={b.at} className="le-bar" onClick={() => n && onPick(b)} title={`${clock(b.at, day)}: ${n.toLocaleString()} lines, ${t.error} errors, ${t.warn} warnings`}>
              <span style={{ height: `${(t.error / max) * 100}%`, background: 'var(--d-red)' }} />
              <span style={{ height: `${(t.warn / max) * 100}%`, background: 'var(--d-amber)' }} />
              <span style={{ height: `${((t.info + t.other) / max) * 100}%`, background: 'var(--d-blue)' }} />
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

// parse reads a JSON line's fields, if it is one.
function parse(line: string): Record<string, unknown> | null {
  if (!line.startsWith('{')) return null
  try {
    const v = JSON.parse(line)
    return v && typeof v === 'object' && !Array.isArray(v) ? v : null
  } catch {
    return null
  }
}

function Line({ e, open, setOpen, onFilter, onTrace }: { e: Entry; open: boolean; setOpen: (o: boolean) => void; onFilter: (f: Filter) => void; onTrace: (id: string) => void }) {
  const [find, setFind] = useState('')
  const json = parse(e.line)
  const message = json ? String(json.msg ?? json.message ?? e.line) : e.line
  const level = levelOf(e.labels.detected_level ?? e.labels.level)
  const trace = e.labels.trace_id ?? (typeof json?.trace_id === 'string' ? json.trace_id : undefined)
  const service = e.labels.service_name ?? ''
  const record: [string, unknown, string?][] = [
    ['time', new Date(e.at).toISOString()],
    ['level', e.labels.detected_level ?? '', 'detected_level'],
    ['service.name', service, 'service_name'],
    ['message', message],
    ...Object.entries(e.labels)
      .filter(([k]) => !['detected_level', 'service_name'].includes(k))
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([k, v]) => [k, v, k] as [string, unknown, string]),
    ...Object.entries(json ?? {})
      .filter(([k]) => !(k in e.labels) && k !== 'msg' && k !== 'message')
      .map(([k, v]) => [k, v, typeof v === 'string' || typeof v === 'number' ? k : undefined] as [string, unknown, string?]),
  ]
  const q = find.trim().toLowerCase()
  const shown = record.filter(([k, v]) => v !== '' && v != null && (!q || k.toLowerCase().includes(q) || String(v).toLowerCase().includes(q)))
  return (
    <div className={'le-line ' + (level === 'error' ? 'error' : level === 'warn' ? 'warn' : '') + (open ? ' open' : '')}>
      <div className="t-row" role="button" tabIndex={0} onClick={() => setOpen(!open)} onKeyDown={(ev) => (ev.key === 'Enter' || ev.key === ' ') && (ev.preventDefault(), setOpen(!open))} aria-expanded={open}>
        <span className={'le-chev' + (open ? ' down' : '')}>
          <Disclosure />
        </span>
        <span className="mono">{clock(e.at, false, true)}</span>
        <span className="lg-level">
          <span className="a-dot" style={{ background: level === 'error' ? 'var(--d-red)' : level === 'warn' ? 'var(--d-amber)' : 'var(--d-faint)' }} />
          {level === 'error' ? 'Error' : level === 'warn' ? 'Warning' : level === 'info' ? 'Info' : e.labels.detected_level || 'Log'}
        </span>
        <span className="mono lg-svc">
          <span className="a-dot" style={{ background: serviceColor(service) }} />
          {service}
        </span>
        <span className="a-name">{message}</span>
        {trace ? (
          <button
            className="mono lg-trace"
            title="Open the trace"
            onClick={(ev) => {
              ev.stopPropagation()
              onTrace(trace)
            }}
          >
            {trace.slice(0, 8)}
          </button>
        ) : (
          <span />
        )}
      </div>
      {open && (
        <div className="le-record">
          <div className="le-record-head">
            <label className="le-input lg-find">
              <Search />
              <input value={find} onChange={(ev) => setFind(ev.target.value)} placeholder="Search properties" aria-label="Search properties" />
            </label>
            {trace && (
              <button className="btn primary medium" onClick={() => onTrace(trace)}>
                View trace
              </button>
            )}
            <button className="btn quiet medium" onClick={() => copy(JSON.stringify(Object.fromEntries(record.filter(([, v]) => v !== '' && v != null).map(([k, v]) => [k, v])), null, 2))}>
              <Copy />
              Copy
            </button>
          </div>
          <div className="lg-json mono">
            <div>{'{'}</div>
            {shown.map(([k, v, f], i) => (
              <div key={k} className="lg-kv">
                <span className="lg-k">"{k}"</span>:{' '}
                {f ? (
                  <button className={typeof v === 'string' ? 'lg-s' : 'lg-n'} onClick={() => onFilter({ field: f, value: String(v) })} title="Only lines like this">
                    {typeof v === 'string' ? JSON.stringify(v) : String(v)}
                  </button>
                ) : (
                  <span className={typeof v === 'string' ? 'lg-s' : 'lg-n'}>{typeof v === 'string' ? JSON.stringify(v) : JSON.stringify(v)}</span>
                )}
                {i < shown.length - 1 ? ',' : ''}
              </div>
            ))}
            <div>{'}'}</div>
          </div>
        </div>
      )}
    </div>
  )
}

async function copy(text: string) {
  try {
    await navigator.clipboard.writeText(text)
    toast('Copied the line')
  } catch {
    toast('Couldn’t copy', { tone: 'failed' })
  }
}

function clock(ms: number, day = false, seconds = false) {
  const d = new Date(ms)
  const t = d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: seconds ? '2-digit' : undefined, hour12: false })
  return day ? d.toLocaleDateString(undefined, { day: 'numeric', month: 'short' }) : seconds ? `${t}.${String(d.getMilliseconds()).padStart(3, '0')}` : t
}
