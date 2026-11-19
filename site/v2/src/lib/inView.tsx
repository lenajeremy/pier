import { createElement, useEffect, useRef, useState, type CSSProperties, type HTMLAttributes, type ReactNode } from 'react'

export const motionEnabled = () =>
  typeof document !== 'undefined' && document.documentElement.classList.contains('motion')

/**
 * seen: true from the first time the element scrolls into view (plays
 * one-shot animations). visible: true only while it's on screen (runs
 * loops). Both are true straight away when motion is off, so nothing waits.
 */
export function useInView<T extends Element = HTMLDivElement>({
  threshold = 0,
  rootMargin = '0px 0px -12% 0px',
  live = false,
}: { threshold?: number; rootMargin?: string; live?: boolean } = {}) {
  const ref = useRef<T>(null)
  const [seen, setSeen] = useState(false)
  const [visible, setVisible] = useState(false)

  useEffect(() => {
    const el = ref.current
    if (!el) return
    if (!motionEnabled() || typeof IntersectionObserver === 'undefined') {
      setSeen(true)
      setVisible(true)
      return
    }
    const io = new IntersectionObserver(
      ([entry]) => {
        if (entry.isIntersecting) {
          setSeen(true)
          setVisible(true)
          if (!live) io.disconnect()
        } else {
          setVisible(false)
        }
      },
      { threshold, rootMargin },
    )
    io.observe(el)
    return () => io.disconnect()
  }, [threshold, rootMargin, live])

  return { ref, seen, visible }
}

type Tag = 'div' | 'section' | 'li' | 'ul' | 'ol' | 'footer' | 'p'

/** A wrapper that marks itself [data-in] (and [data-live] if asked) for motion.css. */
export function InView({
  as = 'div',
  live = false,
  threshold,
  rootMargin,
  children,
  ...rest
}: { as?: Tag; live?: boolean; threshold?: number; rootMargin?: string; children?: ReactNode } & HTMLAttributes<HTMLElement>) {
  const { ref, seen, visible } = useInView<HTMLElement>({ threshold, rootMargin, live })
  return createElement(
    as,
    { ref, 'data-in': seen ? '' : undefined, 'data-live': live && visible ? '' : undefined, ...rest },
    children,
  )
}

/** Inline style for an animation delay, with optional extra custom properties. */
export function d(ms: number, extra: Record<string, string | number> = {}): CSSProperties {
  return { '--d': `${ms}ms`, ...extra } as CSSProperties
}
