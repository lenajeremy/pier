import { useEffect, useImperativeHandle, useRef, type CSSProperties, type Ref } from 'react'
import { FlapRow, reducedMotion, type Column } from './engine'
import './flap.css'

export type FlapHandle = {
  show: (values: string[], opts?: Parameters<FlapRow['show']>[1]) => Promise<void[]>
  set: (values: string[], inks?: (string | undefined)[]) => void
}

/**
 * One row of split-flap tiles. It starts blank and flips to `values` when
 * `play` turns true (or at once with reduced motion). Later changes to
 * `values` flip only the tiles that change. The text is exposed to assistive
 * tech as a label; the tiles themselves are hidden from it.
 */
export function Flaps({
  columns,
  values,
  inks,
  play = true,
  delay = 0,
  stagger,
  className,
  size = 'md',
  label,
  handle,
}: {
  columns: Column[]
  values: string[]
  inks?: (string | undefined)[]
  play?: boolean
  delay?: number
  stagger?: number
  className?: string
  size?: 'xl' | 'lg' | 'md' | 'sm' | 'xs'
  label?: string
  handle?: Ref<FlapHandle>
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
    if (!r || !play) return
    if (reducedMotion()) {
      r.set(values, inks)
      shown.current = true
      return
    }
    r.show(values, { start: shown.current ? 0 : delay, stagger, inks })
    shown.current = true
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, play])

  useImperativeHandle(handle, () => ({
    show: (v, o) => row.current?.show(v, o) ?? Promise.resolve([]),
    set: (v, i) => row.current?.set(v, i),
  }))

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
