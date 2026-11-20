import { useMemo, useRef } from 'react'

export type Sample = { at: number; cpu: number; memory: number; disk: number }
export type History = { samples: Sample[]; start: number; end: number; bucket: number }

const W = 600
const H = 64

// UsageChart is one measure of the server over time, as a share of what it
// has: a line from 0 to 100%, broken where pierd wasn't running. Pointing at
// any chart reads the same moment on all three.
export function UsageChart({ history, measure, hover, onHover, label }: {
  history: History
  measure: 'cpu' | 'memory' | 'disk'
  hover: number | null // the index of the sample being read
  onHover: (i: number | null) => void
  label: string
}) {
  const plot = useRef<HTMLDivElement>(null)
  const { samples, start, end, bucket } = history
  const span = Math.max(1, end - start)
  const x = (at: number) => Math.min(1, Math.max(0, (at - start) / span))
  const y = (v: number) => H - (Math.min(100, Math.max(0, v)) / 100) * H

  const paths = useMemo(() => {
    // A gap of more than a sample and a half means pierd was off: the line stops there.
    const runs: Sample[][] = []
    for (const s of samples) {
      const run = runs.at(-1)
      if (run && s.at - run.at(-1)!.at <= bucket * 1.5) run.push(s)
      else runs.push([s])
    }
    const line = runs
      .map((run) => run.map((s, i) => `${i ? 'L' : 'M'}${(x(s.at) * W).toFixed(1)},${y(s[measure]).toFixed(1)}`).join(' '))
      .join(' ')
    const area = runs
      .filter((run) => run.length > 1)
      .map(
        (run) =>
          `M${(x(run[0].at) * W).toFixed(1)},${H} ` +
          run.map((s) => `L${(x(s.at) * W).toFixed(1)},${y(s[measure]).toFixed(1)}`).join(' ') +
          ` L${(x(run.at(-1)!.at) * W).toFixed(1)},${H} Z`,
      )
      .join(' ')
    return { line, area }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [history, measure])

  const move = (e: React.PointerEvent) => {
    const box = plot.current?.getBoundingClientRect()
    if (!box || samples.length === 0) return
    const at = start + ((e.clientX - box.left) / box.width) * span
    let best = 0
    for (let i = 1; i < samples.length; i++) {
      if (Math.abs(samples[i].at - at) < Math.abs(samples[best].at - at)) best = i
    }
    onHover(best)
  }
  const h = hover !== null ? samples[hover] : null
  const fill = `usage-${measure}`
  return (
    <div
      className="usage-plot"
      ref={plot}
      onPointerMove={move}
      onPointerLeave={() => onHover(null)}
      role="img"
      aria-label={`${label} over time, from 0 to 100%`}
    >
      <div className="chart-grid" aria-hidden>
        <div />
        <div />
        <div className="base" />
      </div>
      <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" className="chart-svg" aria-hidden>
        <defs>
          <linearGradient id={fill} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0" stopColor="#3987E5" stopOpacity="0.24" />
            <stop offset="1" stopColor="#3987E5" stopOpacity="0" />
          </linearGradient>
        </defs>
        <path d={paths.area} fill={`url(#${fill})`} />
        <path d={paths.line} fill="none" stroke="#3987E5" strokeWidth="2" strokeLinejoin="round" strokeLinecap="round" vectorEffect="non-scaling-stroke" />
      </svg>
      {h && (
        <>
          <div className="crosshair" style={{ left: `${x(h.at) * 100}%` }} />
          <div className="marker" style={{ left: `${x(h.at) * 100}%`, top: y(h[measure]), background: '#3987E5' }} />
        </>
      )}
    </div>
  )
}
