import { useRef, useState } from 'react'

// Charts for the Analytics tab: thin lines, one blue for data, red only for
// errors, and the deploys that happened marked on the time axis.

export function Sparkline({ values, height = 28 }: { values: (number | null)[]; height?: number }) {
  const nums = values.map((v) => v ?? 0)
  const lo = Math.min(...nums)
  const hi = Math.max(...nums)
  const w = 200
  const pts = nums.map((v, i) => `${((i / Math.max(1, nums.length - 1)) * w).toFixed(1)},${(height - 2 - ((v - lo) / (hi - lo || 1)) * (height - 4)).toFixed(1)}`)
  return (
    <svg className="a-spark" viewBox={`0 0 ${w} ${height}`} preserveAspectRatio="none" style={{ height }} aria-hidden>
      {pts.length > 1 && <polyline points={pts.join(' ')} fill="none" stroke="var(--d-blue)" strokeWidth="1.5" strokeLinejoin="round" vectorEffect="non-scaling-stroke" />}
    </svg>
  )
}

export interface Deploy {
  at: number
  id: string
}

const clock = (ms: number, withDay: boolean) =>
  new Date(ms).toLocaleString(undefined, withDay ? { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit', hour12: false } : { hour: '2-digit', minute: '2-digit', hour12: false })

// TrafficChart is requests per step as a filled line, errors as red bars
// under it on their own scale, and deploys as dashed lines.
export function TrafficChart({ points, from, to, deploys, onPick }: {
  points: { at: number; requests: number; errors: number }[]
  from: number
  to: number
  deploys: Deploy[]
  onPick?: (at: number) => void
}) {
  const box = useRef<HTMLDivElement>(null)
  const [hover, setHover] = useState<number | null>(null)
  const n = points.length
  const max = niceMax(Math.max(1, ...points.map((p) => p.requests)))
  const emax = Math.max(1, ...points.map((p) => p.errors))
  const W = 1000
  const H = 150
  const x = (i: number) => (n > 1 ? (i / (n - 1)) * W : 0)
  const y = (v: number) => H - (v / max) * H
  const line = points.map((p, i) => `${x(i).toFixed(1)},${y(p.requests).toFixed(1)}`).join(' ')
  const span = to - from
  const withDay = span > 26 * 3600_000
  const pct = (at: number) => ((at - from) / span) * 100
  const ticks = [0, 0.25, 0.5, 0.75, 1].map((f) => from + f * span)
  const move = (e: React.MouseEvent) => {
    const r = box.current?.getBoundingClientRect()
    if (!r || n < 2) return
    setHover(Math.max(0, Math.min(n - 1, Math.round(((e.clientX - r.left) / r.width) * (n - 1)))))
  }
  const h = hover !== null ? points[hover] : null
  return (
    <div className="a-traffic">
      <div className="a-y">
        <span style={{ top: 0 }}>{short(max)}</span>
        <span style={{ top: H / 2 }}>{short(max / 2)}</span>
        <span style={{ top: H }}>0</span>
        <span className="a-y-errors" style={{ top: H + 26 }}>Errors</span>
      </div>
      <div className="a-plot" ref={box} onMouseMove={move} onMouseLeave={() => setHover(null)} onClick={() => h && onPick?.(h.at)} style={{ cursor: onPick ? 'pointer' : undefined }}>
        <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" style={{ height: H }} aria-hidden>
          {[0.25, 0.5, 0.75].map((f) => <line key={f} x1="0" x2={W} y1={H * f} y2={H * f} className="a-grid" vectorEffect="non-scaling-stroke" />)}
          {n > 1 && <polygon points={`0,${H} ${line} ${W},${H}`} fill="var(--d-blue)" fillOpacity="0.12" />}
          {n > 1 && <polyline points={line} fill="none" stroke="var(--d-blue)" strokeWidth="1.75" strokeLinejoin="round" vectorEffect="non-scaling-stroke" />}
          <line x1="0" x2={W} y1={H} y2={H} className="a-base" vectorEffect="non-scaling-stroke" />
        </svg>
        <div className="a-errors">
          {points.map((p, i) => (
            <span key={p.at} style={{ height: `${(p.errors / emax) * 100}%`, opacity: p.errors ? 1 : 0, width: `${100 / n}%`, left: `${(i / n) * 100}%` }} />
          ))}
        </div>
        {deploys.filter((d) => d.at >= from && d.at <= to).map((d) => (
          <div key={d.id} className={'a-deploy' + (pct(d.at) > 88 ? ' left' : '')} style={{ left: `${pct(d.at)}%` }}>
            <span>{d.id.slice(0, 6)}</span>
          </div>
        ))}
        {h && hover !== null && (
          <>
            <div className="a-cross" style={{ left: `${(hover / Math.max(1, n - 1)) * 100}%` }} />
            <div className={'a-tip' + (hover > n * 0.7 ? ' left' : '')} style={{ left: `${(hover / Math.max(1, n - 1)) * 100}%` }}>
              <div className="a-tip-head">{clock(h.at, withDay)}</div>
              <div><i style={{ background: 'var(--d-blue)' }} />Requests<b>{h.requests.toLocaleString()}</b></div>
              <div><i style={{ background: 'var(--d-red)' }} />Errors<b>{h.errors.toLocaleString()}</b></div>
            </div>
          </>
        )}
      </div>
      <div className="a-x">
        {ticks.map((t, i) => (
          <span key={i}>{clock(t, withDay)}</span>
        ))}
      </div>
    </div>
  )
}

function niceMax(v: number) {
  const p = 10 ** Math.floor(Math.log10(v))
  for (const m of [1, 1.2, 1.5, 2, 2.5, 3, 4, 5, 6, 8, 10]) if (m * p >= v) return m * p
  return 10 * p
}

export function short(v: number) {
  if (v >= 1e6) return `${+(v / 1e6).toFixed(1)}m`
  if (v >= 1e3) return `${+(v / 1e3).toFixed(1)}k`
  return `${Math.round(v)}`
}
