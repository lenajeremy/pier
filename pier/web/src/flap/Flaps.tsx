import { useEffect, useRef, type CSSProperties } from 'react'
import { FlapRow, type Column } from './engine'
import './flap.css'

// Flaps is one row of split-flap tiles, from the website's flap engine. In
// the dashboard a row shows its value at once when the page opens, and flips
// only when the value really changes: a build ends, production moves. Only
// the tiles whose letter changes flip. Screen readers get the label.
export function Flaps({
  columns,
  values,
  inks,
  size = 'xs',
  label,
  className,
}: {
  columns: Column[]
  values: string[]
  inks?: (string | undefined)[]
  size?: 'xl' | 'lg' | 'md' | 'sm' | 'xs'
  label?: string
  className?: string
}) {
  const box = useRef<HTMLDivElement>(null)
  const row = useRef<FlapRow | null>(null)
  const shown = useRef(false)

  useEffect(() => {
    if (!box.current) return
    row.current = new FlapRow(box.current, columns)
    return () => {
      row.current?.el.remove()
      row.current = null
      shown.current = false
    }
    // Columns are fixed for a row's life.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const key = values.join('\u0000') + '|' + (inks ?? []).join(',')
  useEffect(() => {
    const r = row.current
    if (!r) return
    if (!shown.current) {
      r.set(values, inks)
      shown.current = true
      return
    }
    r.show(values, { inks })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key])

  return (
    <div
      ref={box}
      className={['flaps', `flaps-${size}`, className].filter(Boolean).join(' ')}
      role="img"
      aria-label={label ?? values.filter(Boolean).join(', ')}
      style={{ '--cols': columns.length } as CSSProperties}
    />
  )
}

// Status inks, from Foundations 2: a status word is inked in its colour.
export const statusInk: Record<string, string> = {
  live: '#F49CCB',
  building: '#BCAEF8',
  failed: '#FFB4B4',
  ready: '#62D8C6',
  none: '#8E96AA',
}

// StatusFlaps is a status word. It has eight tiles so BUILDING fits, but
// empty ones are hidden: LIVE shows four tiles, and the other four appear
// only while a longer word flips in.
export function StatusFlaps({ status, word }: { status: string; word: string }) {
  return (
    <Flaps
      size="sm"
      columns={[{ width: 8 }]}
      values={[word]}
      inks={[statusInk[status]]}
      label={word}
      className="flaps-trim"
    />
  )
}
