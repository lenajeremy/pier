import { useEffect, useRef, useState, type CSSProperties } from 'react'
import { Flaps } from '../flap/Flaps'
import { Fit } from '../components/Fit'
import { Heading } from '../components/Heading'
import { gsap, motionOn } from '../lib/motion'
import { INK } from '../story'
import s from './Rollback.module.css'

const CMD = 'pier rollback blog'
const kept = [
  { id: '8f3a2c', text: 'Add search page, 2 hours ago' },
  { id: '41d0e9', text: 'Fix RSS dates, yesterday' },
  { id: '7a2b10', text: 'Bump astro to 4.16, 3 days ago' },
  { id: '5e7a33', text: 'Add tag pages, 5 days ago' },
]

/** The main set piece. One command, and in the blog row only the deployment
 * column flips back. The counters flip in, and the production frame slides to
 * the deployment it now serves. Scrolling back undoes it. */
export function Rollback() {
  const section = useRef<HTMLElement>(null)
  const [typed, setTyped] = useState(motionOn() ? 0 : CMD.length)
  const [rolled, setRolled] = useState(!motionOn())

  useEffect(() => {
    const el = section.current
    if (!el || !motionOn()) return
    const state = { p: 0 }
    let lastTyped = -1
    let lastRolled = false
    const render = () => {
      const n = Math.round(Math.min(1, state.p / 0.3) * CMD.length)
      if (n !== lastTyped) setTyped((lastTyped = n))
      const r = state.p >= 0.4
      if (r !== lastRolled) setRolled((lastRolled = r))
    }
    const mm = gsap.matchMedia()
    mm.add('(min-width: 1024px)', () => {
      const tw = gsap.to(state, {
        p: 1,
        ease: 'none',
        onUpdate: render,
        scrollTrigger: { trigger: el, start: 'top top', end: '+=100%', scrub: 0.5, pin: true, anticipatePin: 1 },
      })
      return () => tw.kill()
    })
    mm.add('(max-width: 1023px)', () => {
      const tw = gsap.to(state, { p: 1, duration: 2.4, ease: 'none', onUpdate: render, scrollTrigger: { trigger: el, start: 'top 55%', once: true } })
      return () => tw.kill()
    })
    return () => mm.revert()
  }, [])

  const deployment = rolled ? '41d0e9' : '8f3a2c'
  return (
    <section ref={section} id="rollback" className={s.section} aria-labelledby="rollback-title">
      <div className={s.head}>
        <div className={s.copy}>
          <Heading id="rollback-title" text="Roll back without rebuilding." className={s.title} />
          <p className={s.lede}>
            Every deployment is kept. A rollback points production back at an older one, so it takes a third of a second
            and builds nothing.
          </p>
        </div>
        <div className={s.counters}>
          <div className={s.counter}>
            <Flaps size="lg" columns={[{ width: 4 }]} values={[rolled ? '0.3S' : '']} stagger={60} label={rolled ? '0.3 seconds' : ''} />
            <p>to switch production</p>
          </div>
          <div className={s.counter}>
            <Flaps size="lg" columns={[{ width: 1 }]} values={[rolled ? '0' : '']} label={rolled ? '0 builds' : ''} />
            <p>builds</p>
          </div>
        </div>
      </div>

      <div className={s.stage}>
        <div className={s.command}>
          <p className={s.typed}>
            <span className={s.prompt}>$ </span>
            {CMD.slice(0, typed)}
            {typed < CMD.length && <span className={s.caret} aria-hidden="true" />}
          </p>
          <p className={s.result} data-on={rolled ? '' : undefined}>
            production now serves 41d0e9
          </p>
        </div>

        <Fit className={s.rowWide}>
          <div className="housing">
            <Flaps
              size="lg"
              columns={[{ width: 21 }, { width: 6 }, { width: 4 }]}
              values={['blog.pier.example.com', deployment, 'live']}
              inks={[undefined, undefined, INK.pink]}
              stagger={40}
              label={`blog.pier.example.com serves ${deployment}`}
            />
          </div>
        </Fit>
        <Fit className={s.rowNarrow}>
          <div className="housing">
          <Flaps
            size="sm"
            columns={[{ width: 4 }, { width: 6 }, { width: 4 }]}
            values={['blog', deployment, 'live']}
            inks={[undefined, undefined, INK.pink]}
            stagger={40}
            label={`blog serves ${deployment}`}
          />
          </div>
        </Fit>

        <div className={s.kept}>
          <p className={s.keptLabel}>Deployments kept for blog, newest first</p>
          <div className={s.keptGrid} style={{ '--i': rolled ? 1 : 0 } as CSSProperties}>
            {kept.map((k, i) => (
              <div key={k.id} className={s.keptCard}>
                <div className={s.keptTop}>
                  <code>{k.id}</code>
                  <span className={s.prod} data-on={(rolled ? 1 : 0) === i ? '' : undefined}>
                    production
                  </span>
                </div>
                <p>{k.text}</p>
              </div>
            ))}
            <div className={s.frame} aria-hidden="true" />
          </div>
        </div>
      </div>
    </section>
  )
}
