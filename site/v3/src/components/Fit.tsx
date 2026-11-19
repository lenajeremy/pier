import { useLayoutEffect, useRef, useState, type ReactNode } from 'react'

/** Scales its content down (never up) to fit the width it's given, keeping the
 * board's tiles in proportion instead of letting them overflow. */
export function Fit({ children, className, max = 1 }: { children: ReactNode; className?: string; max?: number }) {
  const outer = useRef<HTMLDivElement>(null)
  const inner = useRef<HTMLDivElement>(null)
  const [box, setBox] = useState({ scale: 1, h: 0 })

  useLayoutEffect(() => {
    const o = outer.current
    const i = inner.current
    if (!o || !i) return
    const measure = () => {
      const natural = i.offsetWidth
      const scale = natural ? Math.min(max, o.clientWidth / natural) : 1
      setBox({ scale, h: i.offsetHeight * scale })
    }
    measure()
    const ro = new ResizeObserver(measure)
    ro.observe(o)
    ro.observe(i)
    return () => ro.disconnect()
  }, [max])

  return (
    <div ref={outer} className={className} style={{ width: '100%', height: box.h || undefined, display: 'flex', justifyContent: 'center', alignItems: 'flex-start' }}>
      <div ref={inner} style={{ flex: 'none', transform: `scale(${box.scale})`, transformOrigin: 'top center' }}>
        {children}
      </div>
    </div>
  )
}
