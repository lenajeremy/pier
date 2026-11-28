import { useEffect, useState, type ReactNode } from 'react'
import { ChevronLeft, ChevronRight, Close } from './Icons'

// The panel's width, kept in this browser, shared by every panel.
const KEY = 'pier.rowPanelWidth'
const MIN = 360
const max = () => Math.max(MIN, Math.round(window.innerWidth * 0.85))
const saved = () => {
  try {
    const w = Number(localStorage.getItem(KEY))
    return w >= MIN ? Math.min(w, max()) : 480
  } catch {
    return 480
  }
}

// Sheet is a panel over the right of the page: a database row or a file.
// The page behind is dimmed and out of reach, and a click there closes it.
// Its left edge drags to make it wider, and the arrow keys step through
// the list it was opened from.
export function Sheet({ label, title, onClose, onMove, first, last, children, foot }: {
  label: string // what one item is called: "Row", "File"
  title: ReactNode
  onClose: () => void
  onMove?: (d: number) => void
  first?: boolean
  last?: boolean
  children: ReactNode
  foot?: ReactNode
}) {
  const [width, setWidth] = useState(saved)
  useEffect(() => {
    const before = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => {
      document.body.style.overflow = before
    }
  }, [])
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      if ((e.target as HTMLElement)?.closest?.('input, textarea, [role="separator"], [role="dialog"][aria-modal="true"]:not(.db-sheet)')) return
      if (e.key === 'Escape') onClose()
      if (!onMove) return
      if (e.key === 'ArrowDown') (e.preventDefault(), onMove(1))
      if (e.key === 'ArrowUp') (e.preventDefault(), onMove(-1))
    }
    window.addEventListener('keydown', key)
    return () => window.removeEventListener('keydown', key)
  }, [onClose, onMove])
  const keep = (w: number) => {
    const next = Math.min(max(), Math.max(MIN, Math.round(w)))
    setWidth(next)
    try {
      localStorage.setItem(KEY, String(next))
    } catch {
      // A private window forgets the width.
    }
  }
  const drag = (e: React.PointerEvent<HTMLDivElement>) => {
    e.preventDefault()
    const handle = e.currentTarget
    handle.setPointerCapture(e.pointerId)
    document.body.classList.add('resizing')
    const move = (m: PointerEvent) => keep(window.innerWidth - m.clientX)
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
  const noun = label.toLowerCase()
  return (
    <>
      <div className="db-sheet-backdrop" onPointerDown={onClose} aria-hidden />
      <aside className="db-sheet" role="dialog" aria-modal="true" aria-label={label} style={{ width }}>
        <div
          className="db-sheet-resize"
          role="separator"
          aria-orientation="vertical"
          aria-label={`Resize the ${noun} panel`}
          aria-valuenow={width}
          aria-valuemin={MIN}
          aria-valuemax={max()}
          tabIndex={0}
          onPointerDown={drag}
          onDoubleClick={() => keep(480)}
          onKeyDown={(e) => {
            if (e.key === 'ArrowLeft') (e.preventDefault(), keep(width + 32))
            if (e.key === 'ArrowRight') (e.preventDefault(), keep(width - 32))
          }}
        />
        <div className="db-sheet-head">
          <div className="db-sheet-title">{title}</div>
          {onMove && (
            <>
              <button className="icon-btn" aria-label={`${label} above`} disabled={first} onClick={() => onMove(-1)}>
                <ChevronLeft />
              </button>
              <button className="icon-btn" aria-label={`${label} below`} disabled={last} onClick={() => onMove(1)}>
                <ChevronRight />
              </button>
            </>
          )}
          <button className="icon-btn bare" aria-label="Close" onClick={onClose}>
            <Close />
          </button>
        </div>
        {children}
        {foot && <div className="db-sheet-foot">{foot}</div>}
      </aside>
    </>
  )
}
