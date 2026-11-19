import { useEffect, useRef, useState } from 'react'
import { localDay } from '../lib/calendar'

const DAY = 86_400_000
const MAX_DAYS = 183 // the hourly counts are kept 6 months
const LINE_DAYS = 14 // and the lines 14 days

const addMonths = (d: Date, n: number) => new Date(d.getFullYear(), d.getMonth() + n, 1)
const same = (a: Date, b: Date) => a.getTime() === b.getTime()
const fmt = (d: Date) => d.toLocaleDateString('en-GB', { day: 'numeric', month: 'short' })

export type Span = { from: Date; to: Date } // whole days, from's start to to's end

const presets = [
  { label: 'Last 24 hours', days: 1 },
  { label: 'Last 7 days', days: 7 },
  { label: 'Last 30 days', days: 30 },
  { label: 'Last 3 months', days: 91 },
  { label: 'Last 6 months', days: MAX_DAYS },
]

// DateRangePicker is the Date range picker (Components 2), opened from Custom
// on the range control: presets, two months, and what the range will show.
export function DateRangePicker({ value, onApply, onCancel }: {
  value: Span | null
  onApply: (s: Span | '24h') => void
  onCancel: () => void
}) {
  const today = localDay(new Date())
  const oldest = localDay(today, 1 - MAX_DAYS)
  const [from, setFrom] = useState<Date | null>(value?.from ?? null)
  const [to, setTo] = useState<Date | null>(value?.to ?? null)
  const [last24h, setLast24h] = useState(false)
  const [right, setRight] = useState(() => addMonths(value?.to ?? today, 0))
  const dialog = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const previous = document.activeElement
    dialog.current?.focus()
    const dismiss = (event: PointerEvent) => {
      if (!dialog.current?.parentElement?.contains(event.target as Node)) onCancel()
    }
    document.addEventListener('pointerdown', dismiss)
    return () => {
      document.removeEventListener('pointerdown', dismiss)
      if (previous instanceof HTMLElement && dialog.current?.contains(document.activeElement)) previous.focus()
    }
  }, [onCancel])
  const left = addMonths(right, -1)

  const pick = (d: Date) => {
    setLast24h(false)
    if (!from || to) {
      setFrom(d)
      setTo(null)
    } else if (d < from) {
      setTo(from)
      setFrom(d)
    } else {
      setTo(d)
    }
  }
  const preset = (days: number) => {
    setLast24h(days === 1)
    setRight(addMonths(today, 0))
    setFrom(days === 1 ? localDay(new Date(Date.now() - DAY)) : localDay(today, 1 - days))
    setTo(today)
  }
  const end = to ?? from
  const days = from && end ? Math.round((end.getTime() - from.getTime()) / DAY) + 1 : 0
  const firstLine = new Date(Date.now() - LINE_DAYS * DAY)

  const month = (m: Date) => {
    const first = (m.getDay() + 6) % 7 // Monday first
    const count = new Date(m.getFullYear(), m.getMonth() + 1, 0).getDate()
    const cells = []
    for (let i = 0; i < first; i++) cells.push(<span key={'b' + i} />)
    for (let n = 1; n <= count; n++) {
      const d = new Date(m.getFullYear(), m.getMonth(), n)
      const off = d > today || d < oldest
      const isStart = !!from && same(d, from)
      const isEnd = !!end && same(d, end)
      const inside = !!from && !!end && d > from && d < end
      const cls = ['day', off && 'off', inside && 'inside', isStart && 'start', isEnd && 'end'].filter(Boolean).join(' ')
      cells.push(
        <button key={n} className={cls} aria-label={d.toLocaleDateString('en-GB', { dateStyle: 'long' })} disabled={off} onClick={() => pick(d)} aria-pressed={isStart || isEnd || inside}>
          {n}
        </button>,
      )
    }
    return cells
  }
  const canBack = left > addMonths(oldest, 0)
  const canForward = right < new Date(today.getFullYear(), today.getMonth(), 1)

  return (
    <div ref={dialog} tabIndex={-1} onKeyDown={(e) => { if (e.key === 'Escape') onCancel() }} className="picker" role="dialog" aria-label="Choose a date range">
      <div className="picker-body">
        <div className="picker-presets">
          {presets.map((p) => (
            <button key={p.label} onClick={() => preset(p.days)}>
              {p.label}
            </button>
          ))}
        </div>
        <div className="picker-months">
          {[left, right].map((m, i) => (
            <div key={i} className="picker-month">
              <div className="picker-head">
                {i === 0 ? (
                  <button className="nav" disabled={!canBack} onClick={() => setRight(addMonths(right, -1))} aria-label="Earlier month">
                    <svg viewBox="0 0 24 24" fill="none" stroke="#A3ABBE" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" width="18" height="18" aria-hidden>
                      <path d="M15 6l-6 6 6 6" />
                    </svg>
                  </button>
                ) : (
                  <span style={{ width: 18 }} />
                )}
                <span className="t-caption t-strong">{m.toLocaleDateString('en-GB', { month: 'long', year: 'numeric' })}</span>
                {i === 1 ? (
                  <button className="nav" disabled={!canForward} onClick={() => setRight(addMonths(right, 1))} aria-label="Later month">
                    <svg viewBox="0 0 24 24" fill="none" stroke="#A3ABBE" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" width="18" height="18" aria-hidden>
                      <path d="M9 6l6 6-6 6" />
                    </svg>
                  </button>
                ) : (
                  <span style={{ width: 18 }} />
                )}
              </div>
              <div className="picker-days">
                {['M', 'T', 'W', 'T', 'F', 'S', 'S'].map((d, k) => (
                  <span key={k} className="weekday">
                    {d}
                  </span>
                ))}
                {month(m)}
              </div>
            </div>
          ))}
        </div>
      </div>
      <div className="picker-foot">
        <div>
          <span className="t-caption">
            {last24h ? 'Last 24 hours' : from && end ? `${fmt(from)} to ${fmt(end)}, ${days === 1 ? '1 day' : `${days} days`}` : 'Pick the first day'}
          </span>
          <span className="t-note">
            Charts go back 6 months. Log lines are kept for 14 days
            {end && localDay(end, 1) <= firstLine ? ', so only chart totals are available.' : from && from < firstLine ? `, so the list starts on ${fmt(firstLine)}.` : '.'}
          </span>
        </div>
        <button className="btn quiet" onClick={onCancel}>
          Cancel
        </button>
        <button className="btn primary" disabled={!from} onClick={() => from && onApply(last24h ? '24h' : { from, to: end ?? from })}>
          Apply
        </button>
      </div>
    </div>
  )
}
