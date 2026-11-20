import { useState } from 'react'
import { useData } from '../lib/data'

export type Sample = { at: number; cpu: number; memory: number; disk: number }
type History = { samples: Sample[]; start: number; end: number; bucket: number }
export function ServerCharts() {
  const [range, setRange] = useState('24h')
  const { data, error } = useData<History>('/api/server/history?range=' + range, () => true, 60000)
  return (
    <section className="panel ops-feed">
      <header className="ops-row">
        <h2 className="grow">Resource history</h2>
        <select
          className="input ops-select"
          aria-label="Resource history range"
          value={range}
          onChange={(e) => setRange(e.target.value)}
        >
          <option value="24h">Last 24 hours</option>
          <option value="7d">Last 7 days</option>
        </select>
      </header>
      {error && (
        <p className="ops-empty" role="alert">
          {error.message}
        </p>
      )}
      {data &&
        (data.samples.length < 2 ? (
          <p className="ops-empty">Collecting samples. Trends appear after two intervals.</p>
        ) : (
          <div className="resource-charts">
            {(['cpu', 'memory', 'disk'] as const).map((key) => {
              const label = { cpu: 'CPU', memory: 'Memory', disk: 'Disk' }[key]
              const start = Math.floor(data.start / data.bucket) * data.bucket
              const width = 360 / (1 + (data.end - start) / data.bucket)
              return (
                <figure key={key}>
                  <figcaption>
                    {label} <small>Average usage</small>
                  </figcaption>
                  <svg viewBox="0 0 360 68" role="img" aria-label={label + ' usage over ' + range}>
                    {data.samples.map((p) => (
                      <rect
                        key={p.at}
                        x={((p.at - start) / data.bucket) * width}
                        y={65 - p[key] * 0.6}
                        width={Math.max(1, width - 0.8)}
                        height={Math.max(1, p[key] * 0.6)}
                      >
                        <title>{new Date(p.at).toLocaleString() + ' · ' + p[key].toFixed(1) + '%'}</title>
                      </rect>
                    ))}
                  </svg>
                  <small>
                    {new Date(start).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })} ·{' '}
                    {new Date(start).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })}{' '}
                    <span>Now</span>
                  </small>
                </figure>
              )
            })}
          </div>
        ))}
    </section>
  )
}
