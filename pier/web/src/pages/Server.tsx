import { useState } from 'react'
import { api } from '../lib/api'
import { useData } from '../lib/data'
import { exact, plural, size } from '../lib/format'
import { confirm } from '../components/Dialog'
import { UsageChart, type History } from '../components/ServerCharts'
import { toast } from '../components/Toast'
import { PageHeading, Problem, Skeleton } from '../components/ui'
import { Gate, onlyOwner, useRole } from '../lib/role'
import { UpdateEnd, UpdateNote, usePierdUpdate } from '../components/Update'

type Health = {
  os: string
  arch: string
  cores: number
  address: string
  started_at: string
  disk_total: number
  disk_free: number
  disk_percent: number
  memory_total?: number
  memory_used?: number
  cpu?: number
  services: Record<string, string>
  items: { name: string; bytes: number | null }[]
  previews: number
  cache: boolean
  docker_cache?: boolean
}

const osName: Record<string, string> = { darwin: 'macOS', linux: 'Linux', windows: 'Windows' }

// What each thing on the disk is, so a size means something.
const about: Record<string, string> = {
  Deployments: 'The built files of every kept deployment.',
  'Source archives': 'Uploads from pier deploy, kept so a deployment can be built again.',
  Repositories: 'The git history pushed to Pier.',
  'Build logs': 'The log of every build.',
  'Request database': 'Request lines for 14 days, and hourly counts for 6 months.',
  'Build cache': 'Packages kept so installs are faster. Safe to clear; the next builds download them again.',
  'Docker images': 'The images of server apps, and the ones builds run in.',
  'Docker build cache': 'Layers Docker kept from earlier builds. Safe to prune; the next builds start from scratch.',
}
const serviceNote: Record<string, string> = {
  Caddy: 'The web server in front of every site. It gets their certificates.',
  Docker: 'Runs the builds, and the containers of server apps.',
  pierd: 'Pier itself: the dashboard, the API and git pushes.',
}

type Clean = 'previews' | 'cache' | 'docker-cache'

// Server is I1: how full the machine is and how that's changed, what's
// using the disk with what can be cleared, and the services Pier needs.
// version is pierd's own, from the session; an older pierd doesn't say.
export function Server({ version: pierd }: { version?: string }) {
  const { data, error, reload } = useData<Health>('/api/server', () => true, 15000)
  const [range, setRange] = useState<'24h' | '7d'>('24h')
  const history = useData<History>('/api/server/history?range=' + range, () => true, 60000)
  const [hover, setHover] = useState<number | null>(null)
  const { owner } = useRole()
  // The owner can update pierd from here; everyone sees its version.
  const update = usePierdUpdate(owner, reload)

  const clean = (kind: Clean, bytes: number | null) => {
    const freed = bytes ? ` (${size(bytes)})` : ''
    const q =
      kind === 'cache'
        ? {
            title: 'Clear the build cache?',
            body: `Pier deletes the packages it kept to make installs faster${freed}. Nothing that's serving is touched. The next build of each project downloads its packages again.`,
            action: 'Clear cache',
            done: 'Cleared the build cache',
          }
        : kind === 'docker-cache'
          ? {
              title: "Prune Docker's build cache?",
              body: `Docker deletes the layers it kept from earlier builds, and images that nothing uses${freed}. Running containers and the images of kept deployments stay. The next build of each server app starts from scratch.`,
              action: 'Prune cache',
              done: "Pruned Docker's build cache",
            }
          : {
              title: `Remove ${plural(data?.previews ?? 0, 'old preview')}?`,
              body: "Their built files, saved sources and build logs are deleted, so they can't be visited or promoted any more. Production's history is kept.",
              action: 'Remove previews',
              done: 'Removed the old previews',
            }
    return confirm({
      title: q.title,
      body: <p>{q.body}</p>,
      action: q.action,
      working: 'Clearing…',
      danger: kind === 'previews',
      run: async () => {
        await api('/api/server/cleanup/' + kind, { method: 'POST' })
        await reload()
      },
    }).then((ok) => ok && toast(q.done))
  }

  const samples = history.data?.samples ?? []
  const at = hover !== null ? samples[hover] : null
  const read = at
    ? new Date(at.at).toLocaleString('en-GB', { weekday: 'short', hour: '2-digit', minute: '2-digit' })
    : ''

  const items = [...(data?.items ?? [])].sort((a, b) => (b.bytes ?? -1) - (a.bytes ?? -1))
  const largest = Math.max(1, ...items.map((i) => i.bytes ?? 0))
  const listed = items.reduce((n, i) => n + (i.bytes ?? 0), 0)
  const clearable = items.filter((i) => i.name === 'Build cache' || i.name === 'Docker build cache').reduce((n, i) => n + (i.bytes ?? 0), 0)
  const services = Object.entries(data?.services ?? {})

  return (
    <main className="main global">
      <PageHeading
        title="Server"
        note={
          data
            ? `${data.address}. ${osName[data.os] ?? data.os} on ${data.arch}, with ${plural(data.cores, 'CPU')}${
                data.memory_total ? `, ${size(data.memory_total)} of memory` : ''
              } and a ${size(data.disk_total)} disk.`
            : undefined
        }
      >
        <div className="range" role="radiogroup" aria-label="How far back the charts go">
          {(['24h', '7d'] as const).map((r) => (
            <button key={r} className={range === r ? 'on' : ''} role="radio" aria-checked={range === r} onClick={() => setRange(r)}>
              {r}
            </button>
          ))}
        </div>
      </PageHeading>
      {/* While pierd restarts for an update, not answering is expected. */}
      {error && update.phase !== 'updating' && <Problem>{error.message}</Problem>}
      {!data && !error && (
        <div className="panel">
          <Skeleton rows={4} tall />
        </div>
      )}
      {data && (
        <>
          <div className="panel usage">
            <Measure
              label="CPU"
              value={at ? Math.round(at.cpu) + '%' : data.cpu == null ? 'Not measured' : Math.round(data.cpu) + '%'}
              note={at ? read : `Across ${plural(data.cores, 'CPU')}, right now.`}
            >
              {history.data && <UsageChart history={history.data} measure="cpu" hover={hover} onHover={setHover} label="CPU" />}
            </Measure>
            <Measure
              label="Memory"
              level={!at && data.memory_total && data.memory_used != null && data.memory_used / data.memory_total >= 0.9 ? 'warning' : ''}
              value={
                at && data.memory_total
                  ? size((at.memory / 100) * data.memory_total)
                  : data.memory_used == null
                    ? 'Not measured'
                    : size(data.memory_used)
              }
              note={
                at
                  ? read
                  : data.memory_total && data.memory_used != null
                    ? `${Math.round((data.memory_used / data.memory_total) * 100)}% of ${size(data.memory_total)} is in use.`
                    : ''
              }
            >
              {history.data && <UsageChart history={history.data} measure="memory" hover={hover} onHover={setHover} label="Memory" />}
            </Measure>
            <Measure
              label="Disk"
              level={!at && data.disk_percent >= 95 ? 'failed' : !at && data.disk_percent >= 85 ? 'warning' : ''}
              value={Math.round(at ? at.disk : data.disk_percent) + '%'}
              note={
                at
                  ? read
                  : `${size(data.disk_free)} free of ${size(data.disk_total)}.${data.disk_percent >= 85 ? ' Builds fail when it runs out.' : ''}`
              }
            >
              {history.data && <UsageChart history={history.data} measure="disk" hover={hover} onHover={setHover} label="Disk" />}
            </Measure>
            {history.data && samples.length > 1 && (
              <div className="usage-axis t-note" aria-hidden>
                <span>
                  {new Date(history.data.start).toLocaleString('en-GB', {
                    ...(range === '7d' ? { day: 'numeric', month: 'short' } : { weekday: 'short', hour: '2-digit', minute: '2-digit' }),
                  })}
                </span>
                <span>Each chart runs from 0 to 100%, over the last {range === '7d' ? '7 days' : '24 hours'}. Point at one to read all three.</span>
                <span>Now</span>
              </div>
            )}
            {history.data && samples.length < 2 && (
              <div className="usage-axis t-note">
                <span>Pier measures the server every few minutes. The charts appear after its second reading.</span>
              </div>
            )}
          </div>

          <section className="panel">
            <div className="panel-head">
              <h2 className="t-small t-strong">What's using the disk</h2>
              <span className="t-note end">
                {size(listed)} of the {size(data.disk_total - data.disk_free)} in use.
                {clearable > 0 && ` Clearing the caches frees up to ${size(clearable)}.`}
              </span>
            </div>
            {items.map((i) => {
              const action: Clean | null =
                i.name === 'Build cache' && data.cache ? 'cache' : i.name === 'Docker build cache' && data.docker_cache ? 'docker-cache' : null
              return (
                <div className="disk-row" key={i.name}>
                  <div className="what">
                    <span className="t-small">{i.name}</span>
                    {about[i.name] && <span className="t-note">{about[i.name]}</span>}
                  </div>
                  <div className="share" aria-hidden>
                    {i.bytes != null && <i style={{ width: Math.max(1, (i.bytes / largest) * 100) + '%' }} />}
                  </div>
                  <span className="amount">{i.bytes == null ? <span className="t-quiet">Not measured</span> : size(i.bytes)}</span>
                  <span className="act">
                    {action && (
                      <Gate allow={owner} reason={onlyOwner('clean up the server')}>
                        <button className="btn quiet" onClick={() => clean(action, i.bytes)}>
                          {action === 'cache' ? 'Clear' : 'Prune'}
                        </button>
                      </Gate>
                    )}
                  </span>
                </div>
              )
            })}
            <div className="disk-row">
              <div className="what">
                <span className="t-small">Old previews</span>
                <span className="t-note">
                  {data.previews > 0
                    ? `${plural(data.previews, 'preview')} older than 30 days that nothing points at, with ${data.previews === 1 ? 'its' : 'their'} sources and build logs.`
                    : 'None to remove. Previews older than 30 days that nothing points at are listed here.'}
                </span>
              </div>
              <div className="share" aria-hidden />
              <span className="amount">{data.previews > 0 ? data.previews.toLocaleString('en-GB') : ''}</span>
              <span className="act">
                {data.previews > 0 && (
                  <Gate allow={owner} reason={onlyOwner('clean up the server')}>
                    <button className="btn quiet" onClick={() => clean('previews', null)}>
                      Remove
                    </button>
                  </Gate>
                )}
              </span>
            </div>
          </section>

          <section className="panel">
            <div className="panel-head">
              <h2 className="t-small t-strong">Services</h2>
            </div>
            {services.map(([name, state]) => {
              const version = /^\d/.test(state)
              const down = state === 'Unavailable'
              const unused = state.startsWith('Unused')
              // pierd says its release, v0.6.0, or the commit a build from
              // source came from, dev-3f9a2c1.
              const own = name === 'pierd' && pierd && !down ? `Running, version ${pierd.replace(/^v(?=\d)/, '')}` : ''
              return (
                <div className="service-row" key={name}>
                  <i className={'swatch ' + (down ? 'failed' : unused ? '' : 'ready')} />
                  <div className="what">
                    <span className="t-small">{name}</span>
                    <span className="t-note">
                      {serviceNote[name] ?? ''}
                      {name === 'pierd' && (
                        <>
                          {' '}
                          Running since{' '}
                          <time dateTime={data.started_at} data-tip={exact(data.started_at)} data-tip-mono>
                            {new Date(data.started_at).toLocaleString('en-GB', { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' })}
                          </time>
                          .
                        </>
                      )}
                    </span>
                    {name === 'pierd' && <UpdateNote u={update} />}
                  </div>
                  <span className="service-end">
                    <span className={'t-caption' + (down ? ' bad' : ' t-muted')}>
                      {own || (version ? `Running, version ${state}` : down ? 'Not running' : state)}
                    </span>
                    {name === 'pierd' && <UpdateEnd u={update} />}
                  </span>
                </div>
              )
            })}
          </section>
          <Telemetry owner={owner} />
        </>
      )}
    </main>
  )
}

type TelemetryState = {
  available: boolean
  doing?: '' | 'starting' | 'stopping'
  status?: { on: boolean; retention_days: number; servers: { name: string; image: string; state: { state: string; memory_bytes?: number } }[] }
}
const keeps: Record<string, string> = { tempo: 'Keeps traces.', loki: 'Keeps app logs.', prometheus: 'Keeps metrics.' }

// Telemetry turns on the servers that keep apps' traces, metrics and logs.
function Telemetry({ owner }: { owner: boolean }) {
  const t = useData<TelemetryState>('/api/server/telemetry', (d) => !!d.doing, 2000)
  const d = t.data
  if (!d?.available || !d.status) return null
  const on = d.status.on
  const doing = d.doing
  const change = async () => {
    if (on && !(await confirm({ title: 'Turn off traces, metrics and app logs?', body: 'Apps stop sending them on their next deploy. What was kept stays, and comes back if you turn them on again.', action: 'Turn off' }))) return
    try {
      await api('/api/server/telemetry', { method: 'PUT', body: JSON.stringify({ on: !on }) })
      t.reload()
    } catch (e) {
      toast(e instanceof Error ? e.message : "Couldn't change it", { tone: 'failed' })
    }
  }
  return (
    <section className="panel">
      <div className="panel-head">
        <h2 className="t-small t-strong">Traces, metrics and app logs</h2>
        <span className="end">
          <Gate allow={owner} reason={onlyOwner('turn these on or off')}>
            <button className={'btn medium ' + (on ? 'quiet' : 'primary')} disabled={!!doing} onClick={change}>
              {doing === 'starting' ? 'Starting' : doing === 'stopping' ? 'Stopping' : on ? 'Turn off' : 'Turn on'}
            </button>
          </Gate>
        </span>
      </div>
      {!on && doing !== 'starting' ? (
        <p className="tl-off t-note">
          Pier runs Grafana's Tempo, Loki and Prometheus on this server, and every deploy sends its OpenTelemetry to them, each project kept apart. They use up to 1.3 GB of memory and keep {d.status.retention_days} days.
        </p>
      ) : (
        d.status.servers.map((s) => (
          <div className="service-row" key={s.name}>
            <i className={'swatch ' + (s.state.state === 'running' ? 'ready' : doing ? 'building' : 'failed')} />
            <div className="what">
              <span className="t-small">{s.name[0].toUpperCase() + s.name.slice(1)}</span>
              <span className="t-note">
                {keeps[s.name]} <span className="mono">{s.image}</span>
              </span>
            </div>
            <span className="service-end">
              <span className="t-caption t-muted">
                {s.state.state === 'running' ? `Running${s.state.memory_bytes ? `, ${size(s.state.memory_bytes)}` : ''}` : doing === 'starting' ? 'Starting' : 'Not running'}
              </span>
            </span>
          </div>
        ))
      )}
    </section>
  )
}

function Measure({ label, value, note, level, children }: {
  label: string
  value: string
  note: string
  level?: string // 'warning' or 'failed' once it's nearly full
  children?: React.ReactNode
}) {
  return (
    <div className="measure">
      <span className="t-note">{label}</span>
      <span className={'value' + (level ? ' ' + level : '')}>{value}</span>
      <span className="t-note">{note}</span>
      {children}
    </div>
  )
}
