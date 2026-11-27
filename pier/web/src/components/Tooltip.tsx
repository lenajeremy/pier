import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'

// Tooltips are the one light surface (Components 4): small, short, after
// 400ms of hover or at once on keyboard focus. Any element says what it
// shows with data-tip; data-tip-mono sets it in mono, for exact times.
// data-tip-cut shows the element's own text, only when it's cut off.
type Tip = { text: string; mono: boolean; x: number; y: number; below: boolean }

export function TooltipHost() {
  const [tip, setTip] = useState<Tip | null>(null)
  const timer = useRef<number | undefined>(undefined)
  const box = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const target = (e: Event) => (e.target as Element | null)?.closest?.<HTMLElement>('[data-tip], [data-tip-cut]') ?? null
    const show = (el: HTMLElement) => {
      const cut = 'tipCut' in el.dataset
      if (cut && el.scrollWidth <= el.clientWidth) return
      const text = cut ? el.textContent?.trim() : el.dataset.tip
      if (!text) return
      const r = el.getBoundingClientRect()
      const below = r.top < 44
      setTip({ text, mono: 'tipMono' in el.dataset, x: r.left + r.width / 2, y: below ? r.bottom + 6 : r.top - 6, below })
    }
    const hide = () => {
      window.clearTimeout(timer.current)
      setTip(null)
    }
    const over = (e: MouseEvent) => {
      const el = target(e)
      window.clearTimeout(timer.current)
      if (!el) return setTip(null)
      timer.current = window.setTimeout(() => show(el), 400)
    }
    const focus = (e: FocusEvent) => {
      const el = target(e)
      // Only keyboard focus: a click already shows what the control does.
      if (el && el.matches(':focus-visible')) show(el)
    }
    document.addEventListener('mouseover', over)
    document.addEventListener('focusin', focus)
    document.addEventListener('focusout', hide)
    document.addEventListener('mousedown', hide)
    document.addEventListener('keydown', hide)
    window.addEventListener('scroll', hide, true)
    return () => {
      window.clearTimeout(timer.current)
      document.removeEventListener('mouseover', over)
      document.removeEventListener('focusin', focus)
      document.removeEventListener('focusout', hide)
      document.removeEventListener('mousedown', hide)
      document.removeEventListener('keydown', hide)
      window.removeEventListener('scroll', hide, true)
    }
  }, [])

  // Keep it inside the window's edges.
  useEffect(() => {
    const el = box.current
    if (!el || !tip) return
    const half = el.offsetWidth / 2
    const x = Math.min(window.innerWidth - half - 8, Math.max(half + 8, tip.x))
    el.style.left = x + 'px'
  }, [tip])

  if (!tip) return null
  return createPortal(
    <div
      ref={box}
      className={'tooltip' + (tip.mono ? ' mono' : '')}
      role="tooltip"
      style={{ left: tip.x, top: tip.y, transform: `translate(-50%, ${tip.below ? '0' : '-100%'})` }}
    >
      {tip.text}
    </div>,
    document.body,
  )
}
