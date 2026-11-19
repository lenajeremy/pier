import { useEffect, useRef } from 'react'
import { gsap, motionOn } from '../lib/motion'
import frontends from '../figures/frontends.svg?raw'
import functions from '../figures/functions.svg?raw'
import services from '../figures/services.svg?raw'

const SVG = { frontends, functions, services }

/** A Linear-style line figure. On entry its solid edges draw themselves and
 * the dashed ones fade in; then each figure keeps one small motion that says
 * what it's about. */
export function Figure({ kind, className }: { kind: keyof typeof SVG; className?: string }) {
  const box = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const el = box.current
    if (!el || !motionOn()) return
    const ctx = gsap.context(() => {
      const solid = el.querySelectorAll<SVGElement>('.e')
      const dashed = el.querySelectorAll<SVGElement>('.d')
      gsap.set(solid, { strokeDasharray: 1, strokeDashoffset: 1 })
      gsap.set(dashed, { opacity: 0 })
      const tl = gsap.timeline({ scrollTrigger: { trigger: el, start: 'top 80%', once: true } })
      tl.to(solid, { strokeDashoffset: 0, duration: 1.1, ease: 'power2.inOut', stagger: { each: 0.012, from: 'end' } })
      tl.to(dashed, { opacity: 1, duration: 0.6, ease: 'power1.out' }, '-=0.5')
      tl.set(solid, { clearProps: 'strokeDasharray,strokeDashoffset' })

      if (kind === 'frontends') {
        // The built files lift apart as the card scrolls through the screen.
        el.querySelectorAll<SVGGElement>('.sheet').forEach((g) => {
          const i = Number(g.dataset.i)
          gsap.to(g, {
            y: -i * 9,
            ease: 'none',
            scrollTrigger: { trigger: el, start: 'top 85%', end: 'bottom 20%', scrub: 0.8 },
          })
        })
      }

      if (kind === 'functions') {
        // The two containers take turns: one wakes for a request while the
        // other sleeps.
        const a = el.querySelectorAll('.awake, .asleep')
        const b = el.querySelectorAll('.awake2, .asleep2')
        gsap.set(b, { opacity: 0 })
        gsap.timeline({ repeat: -1, delay: 2, repeatDelay: 2.4 })
          .to(a, { opacity: 0, duration: 0.5, ease: 'power1.inOut' })
          .to(b, { opacity: 1, duration: 0.5, ease: 'power1.inOut' }, '<')
          .to(b, { opacity: 0, duration: 0.5, ease: 'power1.inOut' }, '+=2.4')
          .to(a, { opacity: 1, duration: 0.5, ease: 'power1.inOut' }, '<')
      }

      if (kind === 'services') {
        // The health check: a pulse travels round the ring every 10 seconds of
        // the service's life, shown at a calmer 5.
        const p = el.querySelector<SVGCircleElement>('.pulse')
        if (p) {
          const cx = Number(p.dataset.cx)
          const cy = Number(p.dataset.cy)
          const r = Number(p.dataset.r)
          const a = { t: Math.PI * 0.25 }
          gsap.to(a, {
            t: Math.PI * 2.25,
            duration: 5,
            ease: 'none',
            repeat: -1,
            onUpdate: () => {
              p.setAttribute('cx', String(cx + r * (Math.cos(a.t) - Math.sin(a.t))))
              p.setAttribute('cy', String(cy + (r * (Math.cos(a.t) + Math.sin(a.t))) / 2))
            },
          })
        }
      }
    }, el)
    return () => ctx.revert()
  }, [kind])

  return <div ref={box} className={className} dangerouslySetInnerHTML={{ __html: SVG[kind] }} />
}
