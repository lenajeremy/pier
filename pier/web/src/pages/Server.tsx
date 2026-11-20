import { ServerCharts } from '../components/ServerCharts'
import { useState } from 'react'
import { api } from '../lib/api'
import { useData } from '../lib/data'
import { size } from '../lib/format'
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
}
export function Server() {
  const { data, error, reload } = useData<Health>('/api/server', () => true, 15000)
  const [busy, setBusy] = useState(false),
    [notice, setNotice] = useState('')
  async function clean(kind: 'previews' | 'cache' | 'docker-cache') {
    const prompt =
      kind === 'previews'
        ? 'Remove the listed inactive previews and their saved sources and build logs?'
        : kind === 'docker-cache'
          ? 'Prune Docker Buildx layer cache and untagged dangling layers? Running containers and active deployment images are untouched.'
          : 'Clear Pier’s Docker package cache? The next build may take longer.'
    if (!window.confirm(prompt)) return
    setBusy(true)
    setNotice('')
    try {
      await api('/api/server/cleanup/' + kind, { method: 'POST' })
      await reload()
      setNotice('Cleanup complete.')
    } catch (e) {
      setNotice((e as Error).message)
    } finally {
      setBusy(false)
    }
  }
  const svc = Object.entries(data?.services ?? {})
  return (
    <main className="main global operations">
      <header className="page-heading">
        <div>
          <h1 className="t-title">Server</h1>
          {data && (
            <p className="t-caption t-muted">
              {data.address} · {data.os} / {data.arch} · {data.cores} CPUs
            </p>
          )}
        </div>
      </header>
      {error && <p role="alert">{error.message}</p>}
      {data && (
        <>
          <div className="panel ops-metrics">
            {[
              ['Disk', Math.round(data.disk_percent) + '%', size(data.disk_free) + ' free of ' + size(data.disk_total)],
              [
                'Memory',
                data.memory_used == null ? 'Unavailable' : size(data.memory_used),
                data.memory_total ? 'Of ' + size(data.memory_total) : '',
              ],
              ['CPU', data.cpu == null ? 'Unavailable' : Math.round(data.cpu) + '%', 'Across all cores'],
              [
                'Services',
                String(svc.filter(([, v]) => v !== 'Unavailable' && !v.startsWith('Unused')).length) + ' running',
                'Checked on this server',
              ],
            ].map(([k, v, t]) => (
              <div key={k}>
                <small>{k}</small>
                <strong>{v}</strong>
                <small>{t}</small>
              </div>
            ))}
          </div>
          {data.disk_percent >= 85 && (
            <p className="field-message">Disk usage is above 85%. Review the space used below.</p>
          )}
          {data.memory_total && data.memory_used != null && data.memory_used / data.memory_total >= 0.9 && (
            <p className="field-message">Memory usage is above 90%.</p>
          )}
          <ServerCharts />
          <section className="panel ops-feed">
            <h2 className="ops-section-title">What’s using the disk</h2>
            {data.items.map((i) => (
              <div className="ops-row" key={i.name}>
                <div>{i.name}</div>
                {i.bytes != null && (
                  <meter
                    className="disk-share"
                    min={0}
                    max={Math.max(1, ...data.items.map((x) => x.bytes ?? 0))}
                    value={i.bytes}
                    aria-label={i.name + ' relative to the largest category'}
                  />
                )}
                <span className="mono">{i.bytes == null ? 'Size unavailable' : size(i.bytes)}</span>
                {i.name === 'Build cache' && (
                  <button className="btn quiet" disabled={busy} onClick={() => clean('cache')}>
                    Clear cache
                  </button>
                )}
                {i.name === 'Docker build cache' && (
                  <button className="btn quiet" disabled={busy} onClick={() => clean('docker-cache')}>
                    Prune cache
                  </button>
                )}
              </div>
            ))}
            <div className="ops-row">
              <div>
                <strong>Old previews</strong>
                <small>
                  {data.previews} eligible for removal. Includes saved sources and build logs for inactive previews
                  older than 30 days. Production history is kept.
                </small>
              </div>
              <button className="btn quiet" disabled={busy || !data.previews} onClick={() => clean('previews')}>
                Remove previews
              </button>
            </div>
          </section>
          {notice && (
            <p role="status" className="t-caption">
              {notice}
            </p>
          )}
          <section className="panel ops-feed">
            <h2 className="ops-section-title">Services</h2>
            {svc.map(([name, state]) => (
              <div className="ops-row" key={name}>
                <i
                  className={
                    'swatch ' + (state === 'Unavailable' ? 'failed' : state.startsWith('Unused') ? 'asleep' : 'ready')
                  }
                />
                <div>{name}</div>
                <span>{state}</span>
              </div>
            ))}
            <div className="ops-row">
              <div>Pier started</div>
              <time>{new Date(data.started_at).toLocaleString()}</time>
            </div>
          </section>
        </>
      )}
    </main>
  )
}
