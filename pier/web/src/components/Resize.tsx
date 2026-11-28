import { useState, type CSSProperties } from 'react'

// A side panel whose contents can be cut off can be made wider: its edge
// drags, the arrow keys move it, and a double click puts it back. The width
// is remembered in this browser.

export function useWidth(key: string, initial: number, min: number, max: number) {
  const [width, setWidth] = useState(() => {
    try {
      const w = Number(localStorage.getItem(key))
      return w >= min && w <= max ? w : initial
    } catch {
      return initial
    }
  })
  const keep = (w: number) => {
    const next = Math.round(Math.min(max, Math.max(min, w)))
    setWidth(next)
    try {
      localStorage.setItem(key, String(next))
    } catch {
      // A private window forgets it.
    }
  }
  return { width, keep, reset: () => keep(initial), min, max }
}

// Resizer is the panel's edge. A panel on the left of it grows as it moves
// right; one on the right grows as it moves left.
export function Resizer({ w, panel, label, style }: { w: ReturnType<typeof useWidth>; panel: 'left' | 'right'; label: string; style?: CSSProperties }) {
  const grow = panel === 'left' ? 1 : -1
  const drag = (e: React.PointerEvent<HTMLDivElement>) => {
    e.preventDefault()
    const handle = e.currentTarget
    const start = e.clientX
    const from = w.width
    handle.setPointerCapture(e.pointerId)
    document.body.classList.add('resizing')
    const move = (m: PointerEvent) => w.keep(from + grow * (m.clientX - start))
    const up = () => {
      handle.removeEventListener('pointermove', move)
      handle.removeEventListener('pointerup', up)
      handle.removeEventListener('pointercancel', up)
      document.body.classList.remove('resizing')
    }
    handle.addEventListener('pointermove', move)
    handle.addEventListener('pointerup', up)
    handle.addEventListener('pointercancel', up)
  }
  return (
    <div
      className="resizer"
      style={style}
      role="separator"
      aria-orientation="vertical"
      aria-label={label}
      aria-valuenow={w.width}
      aria-valuemin={w.min}
      aria-valuemax={w.max}
      tabIndex={0}
      onPointerDown={drag}
      onDoubleClick={w.reset}
      onKeyDown={(e) => {
        if (e.key === 'ArrowRight') (e.preventDefault(), w.keep(w.width + 24 * grow))
        if (e.key === 'ArrowLeft') (e.preventDefault(), w.keep(w.width - 24 * grow))
      }}
    />
  )
}
