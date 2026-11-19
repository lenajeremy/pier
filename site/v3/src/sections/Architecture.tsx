import { useEffect, useRef, useState } from 'react'
import server from '../figures/server.svg?raw'
import { Heading } from '../components/Heading'
import { gsap, motionOn } from '../lib/motion'
import s from './Architecture.module.css'

const parts = [
  { name: 'pierd', part: 'pierd', color: 'var(--blue)', role: 'The daemon', text: 'The daemon on your server. It takes pushes, runs builds, keeps every deployment in SQLite and serves the API and the dashboard.' },
  { name: 'caddy', part: 'caddy', color: 'var(--pink)', role: 'Routing and HTTPS', text: 'Routes every address to the right deployment and gets a certificate the first time a new domain is visited.' },
  { name: 'docker', part: 'docker', color: 'var(--teal)', role: 'Builds and containers', text: 'Each build runs in a throwaway container. Functions sleep when idle; services run for good, health-checked.' },
  { name: 'dashboard', part: 'pierd', color: 'var(--blue)', role: 'Built into pierd', text: "A web app served by pierd itself, so there's nothing extra to host. It uses the same API as the CLI." },
  { name: 'pier', part: 'laptop', color: 'var(--violet)', role: 'The CLI on your laptop', text: 'Logs, rollbacks, variables and domains from your terminal. Deploys themselves are a plain git push.' },
]

const LABEL_PART: Record<string, string> = {
  pierd: 'pierd',
  docker: 'docker',
  caddy: 'caddy',
  'Your laptop': 'laptop',
  'Your server': 'server',
  Visitors: 'caddy',
}

export function Architecture() {
  const fig = useRef<HTMLDivElement>(null)
  const [open, setOpen] = useState(0)
  const [picked, setPicked] = useState(false)

  // Entrance, packets on the wires and a slight parallax.
  useEffect(() => {
    const el = fig.current
    if (!el) return
    el.querySelectorAll<SVGGElement>('.label').forEach((g) => {
      g.dataset.part = LABEL_PART[g.textContent?.trim() ?? ''] ?? ''
    })
    if (!motionOn()) return
    const svg = el.querySelector('svg')!
    const ctx = gsap.context(() => {
      const solid = el.querySelectorAll<SVGElement>('.e')
      gsap.set(solid, { strokeDasharray: 1, strokeDashoffset: 1 })
      gsap.set(el.querySelectorAll('.d, .label'), { opacity: 0 })
      const tl = gsap.timeline({ scrollTrigger: { trigger: el, start: 'top 75%', once: true } })
      tl.to(solid, { strokeDashoffset: 0, duration: 1.4, ease: 'power2.inOut', stagger: { each: 0.008 } })
        .to(el.querySelectorAll('.d'), { opacity: 1, duration: 0.6 }, '-=0.6')
        .to(el.querySelectorAll('.label'), { opacity: 1, duration: 0.5, stagger: 0.08 }, '-=0.3')
        .set(solid, { clearProps: 'strokeDasharray,strokeDashoffset' })

      // A packet runs along each flow line, on a loop.
      el.querySelectorAll<SVGLineElement>('line[data-flow]').forEach((line, k) => {
        const [x1, y1, x2, y2] = ['x1', 'y1', 'x2', 'y2'].map((a) => Number(line.getAttribute(a)))
        const dot = document.createElementNS('http://www.w3.org/2000/svg', 'circle')
        dot.setAttribute('r', '4')
        dot.setAttribute('fill', line.getAttribute('stroke') ?? '#fff')
        dot.setAttribute('cx', String(x1))
        dot.setAttribute('cy', String(y1))
        dot.setAttribute('opacity', '0')
        dot.dataset.part = line.dataset.flow === 'push' ? 'laptop' : 'caddy'
        svg.appendChild(dot)
        gsap
          .timeline({ repeat: -1, delay: 2 + k * 1.3, repeatDelay: 1.6 })
          .set(dot, { attr: { cx: x1, cy: y1 }, opacity: 0 })
          .to(dot, { opacity: 1, duration: 0.2 })
          .to(dot, { attr: { cx: x2, cy: y2 }, duration: 1.3, ease: 'power1.inOut' }, '<')
          .to(dot, { opacity: 0, duration: 0.25 }, '-=0.15')
      })

      // Subtle depth: the drawing drifts a few pixels against the pointer.
      const qx = gsap.quickTo(svg, 'x', { duration: 1.2, ease: 'power3.out' })
      const qy = gsap.quickTo(svg, 'y', { duration: 1.2, ease: 'power3.out' })
      const move = (e: PointerEvent) => {
        qx((e.clientX / window.innerWidth - 0.5) * -14)
        qy((e.clientY / window.innerHeight - 0.5) * -10)
      }
      window.addEventListener('pointermove', move)
      return () => window.removeEventListener('pointermove', move)
    }, el)
    return () => ctx.revert()
  }, [])

  // Picking a part brings it forward and lets the rest step back.
  useEffect(() => {
    const el = fig.current
    if (!el) return
    const focus = picked ? parts[open].part : null
    el.querySelectorAll<SVGElement>('[data-part]').forEach((g) => {
      const on = !focus || g.dataset.part === focus || g.dataset.part === 'server'
      g.style.transition = 'opacity 300ms ease'
      g.style.opacity = on ? '' : '0.18'
    })
  }, [open, picked])

  return (
    <section id="architecture" className={s.section} aria-labelledby="arch-title">
      <div className={s.head}>
        <Heading id="arch-title" text="Small enough to understand." className={s.title} />
        <p className={s.lede}>One Go daemon and one CLI, on top of Caddy and Docker. There's no cluster to run and no database server to look after.</p>
      </div>
      <div className={s.body}>
        <ul className={s.list}>
          {parts.map((p, i) => {
            const on = open === i
            return (
              <li key={p.name} className={on ? s.on : undefined} style={on ? { boxShadow: `inset 2px 0 0 ${p.color}` } : undefined}>
                <button
                  type="button"
                  className={s.trigger}
                  aria-expanded={on}
                  aria-controls={`part-${i}`}
                  onClick={() => {
                    setOpen(i)
                    setPicked(true)
                  }}
                >
                  <i className={s.swatch} style={{ background: p.color }} />
                  <code className={s.name}>{p.name}</code>
                  <span className={s.role}>{p.role}</span>
                </button>
                <div id={`part-${i}`} className={s.panel} data-open={on ? '' : undefined}>
                  <div>
                    <p className={s.text}>{p.text}</p>
                  </div>
                </div>
              </li>
            )
          })}
        </ul>
        <div ref={fig} className={s.figure} role="img" aria-label="Your server runs Caddy at the front, pierd in the middle and Docker containers at the back. Your laptop pushes to pierd; visitors arrive through Caddy." dangerouslySetInnerHTML={{ __html: server }} />
      </div>
    </section>
  )
}
