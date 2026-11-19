import { useEffect, useRef, useState } from 'react'
import gsap from 'gsap'
import { ScrollTrigger } from 'gsap/ScrollTrigger'
import Lenis from 'lenis'

gsap.registerPlugin(ScrollTrigger)

export { gsap, ScrollTrigger }

/** True when the page may animate: JS ran and the visitor hasn't asked for
 * reduced motion. main.tsx adds the class before the first render. */
export const motionOn = () =>
  typeof document !== 'undefined' && document.documentElement.classList.contains('motion')

/** Smooth scrolling, driven by GSAP's ticker so ScrollTrigger stays in step. */
export function startSmoothScroll() {
  if (!motionOn()) return null
  const lenis = new Lenis({ lerp: 0.1, wheelMultiplier: 1 })
  lenis.on('scroll', ScrollTrigger.update)
  gsap.ticker.add((t) => lenis.raf(t * 1000))
  gsap.ticker.lagSmoothing(0)
  return lenis
}

/** seen: true once the element has entered the viewport. visible: true while
 * it is on screen. Both start true without motion, so nothing waits. */
export function useInView<T extends Element>({ margin = '0px 0px -15% 0px', live = false } = {}) {
  const ref = useRef<T>(null)
  const [seen, setSeen] = useState(false)
  const [visible, setVisible] = useState(false)
  useEffect(() => {
    const el = ref.current
    if (!el) return
    if (!motionOn() || typeof IntersectionObserver === 'undefined') {
      setSeen(true)
      setVisible(true)
      return
    }
    const io = new IntersectionObserver(
      ([e]) => {
        if (e.isIntersecting) {
          setSeen(true)
          setVisible(true)
          if (!live) io.disconnect()
        } else setVisible(false)
      },
      { rootMargin: margin },
    )
    io.observe(el)
    return () => io.disconnect()
  }, [margin, live])
  return { ref, seen, visible }
}

/** A media query as state, for layouts that change their tiles on phones. */
export function useMedia(query: string) {
  const [match, setMatch] = useState(() => typeof window !== 'undefined' && window.matchMedia(query).matches)
  useEffect(() => {
    const m = window.matchMedia(query)
    const on = () => setMatch(m.matches)
    m.addEventListener('change', on)
    return () => m.removeEventListener('change', on)
  }, [query])
  return match
}

/** Section headings blur in word by word when they scroll into view. It is
 * the page's one text effect, used on every h2 and nowhere else. */
export function useBlurIn<T extends HTMLElement>() {
  const ref = useRef<T>(null)
  useEffect(() => {
    const el = ref.current
    if (!el || !motionOn()) return
    const words = el.querySelectorAll('.w')
    const tween = gsap.to(words, {
      opacity: 1,
      filter: 'blur(0px)',
      y: 0,
      duration: 0.8,
      ease: 'power3.out',
      stagger: 0.06,
      scrollTrigger: { trigger: el, start: 'top 85%', once: true },
    })
    return () => {
      tween.scrollTrigger?.kill()
      tween.kill()
    }
  }, [])
  return ref
}
