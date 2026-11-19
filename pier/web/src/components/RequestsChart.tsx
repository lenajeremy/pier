import { useMemo, useRef, useState } from 'react'
import { plural } from '../lib/format'

export type Bucket = { at: number; requests: number; errors: number }
export type ChartData = { from: number; to: number; bucket_ms: number; buckets: Bucket[]; requests: number; errors: number; busiest: Bucket }

const W = 1000
const RH = 140 // requests chart height
const EH = 56 // errors chart height

// RequestsChart is "Requests over time" (Components 6): requests and errors
// as two line charts on one time axis, each with its own scale and its total
// beside it. The bucket still filling is dashed. Hover reads any bucket.
export function RequestsChart({ data, live }: { data: ChartData; live: boolean }) {
  const [hover, setHover] = useState<number | null>(null)
  const plot = useRef<HTMLDivElement>(null)
  const b = data.buckets
  const n = b.length
  const rMax = nice(Math.max(...b.map((x) => x.requests), 1))
  const eMax = Math.max(2, nice(Math.max(...b.map((x) => x.errors), 1)))
  const x = (i: number) => (n <= 1 ? W / 2 : (i / (n - 1)) * W)
  const yR = (v: number) => RH - (v / rMax) * RH
  const yE = (v: number) => EH - (v / eMax) * EH
  const last = live ? n - 1 : n // with a live range, the last bucket is still filling

  const paths = useMemo(() => {
    const line = (pts: [number, number][]) => pts.map(([px, py], i) => `${i ? 'L' : 'M'}${px.toFixed(1)},${py.toFixed(1)}`).join(' ')
    const r = b.map((v, i) => [x(i), yR(v.requests)] as [number, number])
    const e = b.map((v, i) => [x(i), yE(v.errors)] as [number, number])
    const solid = (pts: [number, number][]) => pts.slice(0, Math.max(1, last))
    const area = (pts: [number, number][], h: number) => {
      const s = solid(pts)
      return s.length ? `M${s[0][0]},${h} ` + line(s).replace(/^M/, 'L') + ` L${s[s.length - 1][0]},${h} Z` : ''
    }
    return {
      r: line(solid(r)),
      rArea: area(r, RH),
      rDash: last < n && n > 1 ? line(r.slice(n - 2)) : '',
      e: line(solid(e)),
      eArea: area(e, EH),
      eDash: last < n && n > 1 ? line(e.slice(n - 2)) : '',
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data, live])

  const move = (ev: React.PointerEvent) => {
    const box = plot.current?.getBoundingClientRect()
    if (!box || n === 0) return
    const f = Math.min(1, Math.max(0, (ev.clientX - box.left) / box.width))
    setHover(Math.round(f * (n - 1)))
  }

  const ticks = useMemo(() => {
    const out: { i: number; label: string }[] = []
    if (n < 2) return out
    const count = 4
    for (let k = 0; k < count; k++) {
      const i = Math.round((k / count) * (n - 1))
      out.push({ i, label: when(b[i].at, data.bucket_ms, data.to - data.from) })
    }
    return out
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data])

  const h = hover !== null ? b[hover] : null
  const left = (i: number) => `${(n <= 1 ? 50 : (i / (n - 1)) * 100).toFixed(3)}%`
  const busiest = data.busiest.requests > 0 ? `Busiest ${unit(data.bucket_ms)} ${data.busiest.requests.toLocaleString('en-GB')}` : 'None yet'

  return (
    <section className="panel requests-chart" onPointerLeave={() => setHover(null)}>
      <div className="chart-row">
        <div className="chart-stat">
          <span className="t-caption t-muted">Requests</span>
          <span className="value">{data.requests.toLocaleString('en-GB')}</span>
          <span className="t-note">{busiest}</span>
        </div>
        <Axis top={rMax} height={RH} />
        <div className="chart-plot" ref={plot} style={{ height: RH }} onPointerMove={move}>
          <Grid lines={3} />
          <svg viewBox={`0 0 ${W} ${RH}`} preserveAspectRatio="none" className="chart-svg" aria-hidden>
            <defs>
              <linearGradient id="reqFill" x1="0" y1="0" x2="0" y2="1">
                <stop offset="0" stopColor="#3987E5" stopOpacity="0.28" />
                <stop offset="1" stopColor="#3987E5" stopOpacity="0" />
              </linearGradient>
            </defs>
            <path d={paths.rArea} fill="url(#reqFill)" />
            <path d={paths.r} fill="none" stroke="#3987E5" strokeWidth="2" strokeLinejoin="round" vectorEffect="non-scaling-stroke" />
            {paths.rDash && <path d={paths.rDash} fill="none" stroke="#3987E5" strokeWidth="2" strokeDasharray="4 4" vectorEffect="non-scaling-stroke" />}
          </svg>
          {h && hover !== null && (
            <>
              <div className="crosshair" style={{ left: left(hover) }} />
              <div className="marker" style={{ left: left(hover), top: yR(h.requests), background: '#3987E5' }} />
              <div className="chart-tip" style={hover > n / 2 ? { right: `calc(${100 - (hover / Math.max(1, n - 1)) * 100}% + 14px)` } : { left: `calc(${left(hover)} + 14px)` }}>
                <span className="t-note">{range(h.at, data.bucket_ms)}</span>
                <span className="t-caption">{plural(h.requests, 'request')}</span>
                {h.errors > 0 && (
                  <span className="t-caption" style={{ color: 'var(--d-red-text)' }}>
                    {plural(h.errors, 'error')}
                  </span>
                )}
              </div>
            </>
          )}
        </div>
      </div>
      <div className="chart-row" style={{ paddingTop: 16 }}>
        <div className="chart-stat">
          <span className="t-caption t-muted">Errors</span>
          <div style={{ display: 'flex', alignItems: 'baseline', gap: 8 }}>
            <span className="value">{data.errors.toLocaleString('en-GB')}</span>
            <span className="t-note">4xx and 5xx</span>
          </div>
        </div>
        <Axis top={eMax} height={EH} />
        <div className="chart-plot" style={{ height: EH }} onPointerMove={move}>
          <Grid lines={2} />
          <svg viewBox={`0 0 ${W} ${EH}`} preserveAspectRatio="none" className="chart-svg" aria-hidden>
            <defs>
              <linearGradient id="errFill" x1="0" y1="0" x2="0" y2="1">
                <stop offset="0" stopColor="#FF8080" stopOpacity="0.22" />
                <stop offset="1" stopColor="#FF8080" stopOpacity="0" />
              </linearGradient>
            </defs>
            <path d={paths.eArea} fill="url(#errFill)" />
            <path d={paths.e} fill="none" stroke="#FF8080" strokeWidth="2" strokeLinejoin="round" vectorEffect="non-scaling-stroke" />
            {paths.eDash && <path d={paths.eDash} fill="none" stroke="#FF8080" strokeWidth="2" strokeDasharray="4 4" vectorEffect="non-scaling-stroke" />}
          </svg>
          {h && hover !== null && (
            <>
              <div className="crosshair" style={{ left: left(hover) }} />
              <div className="marker" style={{ left: left(hover), top: yE(h.errors), background: '#FF8080' }} />
            </>
          )}
        </div>
      </div>
      <div className="chart-row">
        <div style={{ width: 194, flex: 'none' }} />
        <div className="chart-ticks">
          {ticks.map((t, k) => (
            <span key={k} className="t-note" style={{ left: left(t.i), transform: k === 0 ? 'none' : 'translateX(-50%)' }}>
              {t.label}
            </span>
          ))}
          <span className="t-note" style={{ right: 0 }}>
            {live ? 'Now' : when(data.to, data.bucket_ms, data.to - data.from)}
          </span>
        </div>
      </div>
    </section>
  )
}

function Axis({ top, height }: { top: number; height: number }) {
  const mid = top / 2
  return (
    <div className="chart-axis" style={{ height }}>
      <span className="t-note" style={{ marginTop: -9 }}>
        {short(top)}
      </span>
      {height > 100 && <span className="t-note">{Number.isInteger(mid) ? short(mid) : ''}</span>}
      <span className="t-note" style={{ marginBottom: -9 }}>
        0
      </span>
    </div>
  )
}

function Grid({ lines }: { lines: number }) {
  return (
    <div className="chart-grid" aria-hidden>
      {Array.from({ length: lines }, (_, i) => (
        <div key={i} className={i === lines - 1 ? 'base' : ''} />
      ))}
    </div>
  )
}

// nice rounds a chart's top up to 1, 2, 2.5 or 5 times a power of ten.
function nice(v: number): number {
  const p = Math.pow(10, Math.floor(Math.log10(v)))
  for (const m of [1, 2, 2.5, 5, 10]) if (v <= m * p) return m * p
  return 10 * p
}

const short = (v: number) => (v >= 1000 ? `${+(v / 1000).toFixed(1)}k` : String(v))

function unit(step: number): string {
  if (step <= 60_000) return 'minute'
  if (step <= 3_600_000) return 'hour'
  return 'day'
}

function when(at: number, step: number, span: number): string {
  const d = new Date(at)
  if (span > 2 * 86_400_000 || step >= 86_400_000) return d.toLocaleDateString('en-GB', { day: 'numeric', month: 'short' })
  return d.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' })
}

function range(at: number, step: number): string {
  const f = (t: number) =>
    step >= 86_400_000
      ? new Date(t).toLocaleDateString('en-GB', { day: 'numeric', month: 'short' })
      : new Date(t).toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' })
  return step >= 86_400_000 ? f(at) : `${f(at)} to ${f(at + step)}`
}
