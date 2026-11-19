import { useEffect, useRef, useState } from 'react'
import { Flaps } from '../flap/Flaps'
import { Heading } from '../components/Heading'
import { Fit } from '../components/Fit'
import { gsap, motionOn, ScrollTrigger } from '../lib/motion'
import { COLS, INK, pushLog, steps } from '../story'
import s from './Push.module.css'

const END = 11.2

/** The first held scene. The scroll is the push's clock: log lines print as
 * their time comes, the steps light in turn, and the new row flips to
 * PREVIEW when the address goes live. */
export function Push() {
  const section = useRef<HTMLElement>(null)
  const clock = useRef<HTMLSpanElement>(null)
  const lines = useRef<(HTMLParagraphElement | null)[]>([])
  const [step, setStep] = useState(motionOn() ? -1 : steps.length - 1)
  const [live, setLive] = useState(!motionOn())

  useEffect(() => {
    const el = section.current
    if (!el || !motionOn()) return
    const state = { t: 0 }
    let lastStep = -2
    let lastLive = false
    const render = () => {
      const t = state.t
      if (clock.current) clock.current.textContent = t.toFixed(2) + 's'
      pushLog.forEach((l, i) => lines.current[i]?.classList.toggle(s.shown, parseFloat(l.at) <= t + 0.001))
      const st = t <= 0 ? -1 : steps.findIndex((x) => t <= x.until)
      const idx = st === -1 && t > 0 ? steps.length - 1 : st
      if (idx !== lastStep) setStep((lastStep = idx))
      const isLive = t >= END - 0.001
      if (isLive !== lastLive) setLive((lastLive = isLive))
    }
    render()

    const mm = gsap.matchMedia()
    mm.add('(min-width: 1024px)', () => {
      const tw = gsap.to(state, {
        t: END,
        ease: 'none',
        onUpdate: render,
        scrollTrigger: { trigger: el, start: 'top top', end: '+=140%', scrub: 0.6, pin: true, anticipatePin: 1 },
      })
      return () => tw.kill()
    })
    mm.add('(max-width: 1023px)', () => {
      const tw = gsap.to(state, {
        t: END,
        duration: 4.5,
        ease: 'power1.inOut',
        onUpdate: render,
        scrollTrigger: { trigger: el, start: 'top 60%', once: true },
      })
      return () => tw.kill()
    })
    ScrollTrigger.refresh()
    return () => mm.revert()
  }, [])

  return (
    <section ref={section} id="push" className={s.section} aria-labelledby="push-title">
      <div className={s.inner}>
        <div className={s.copy}>
          <Heading id="push-title" text="One push, one new row." className={s.title} />
          <p className={s.lede}>
            Push any branch. Pier builds it in a throwaway container, keeps the result as a deployment that never
            changes, and gives it its own address with HTTPS.
          </p>
          <ol className={s.steps}>
            {steps.map((x, i) => (
              <li key={x.title} className={i === step ? s.active : i < step ? s.done : undefined}>
                <Flaps size="sm" columns={[{ width: 2 }]} values={[`0${i + 1}`]} delay={0} stagger={40} label={`Step ${i + 1}`} />
                <div className={s.stepText}>
                  <p className={s.stepTitle}>{x.title}</p>
                  <p className={s.stepSub}>{x.text}</p>
                </div>
                <p className={s.stepTime}>{x.time}</p>
              </li>
            ))}
          </ol>
        </div>

        <div className={s.terminal} aria-label="The git push, line by line">
          <div className={s.termBar}>
            <span>~/blog</span>
            <span ref={clock} className={s.clock}>
              {motionOn() ? '0.00s' : '11.20s'}
            </span>
          </div>
          <div className={s.log}>
            {pushLog.map((l, i) => (
              <p
                key={i}
                ref={(e) => {
                  lines.current[i] = e
                }}
                className={[s.line, l.strong ? s.strong : '', motionOn() ? '' : s.shown].join(' ')}
              >
                <span className={s.at}>{l.at.padStart(5, ' ')}</span>
                {l.tag ? (
                  <>
                    <span className={s.remote}>remote: </span>
                    <span style={{ color: l.tag[1] }}>{l.tag[0].padEnd(7, ' ')}</span>
                  </>
                ) : null}
                {l.text}
              </p>
            ))}
          </div>
        </div>
      </div>

      <div className={s.rowWrap}>
        <p className={s.rowCaption}>The row this push adds to the board. It flips to PREVIEW when the address answers.</p>
        <Fit className={s.rowWide}>
          <div className="housing">
            <Flaps
              size="md"
              columns={[{ width: COLS.address }, { width: COLS.deployment }, { width: COLS.branch }, { width: COLS.status }]}
              values={['feat-search--blog', 'c19e77', 'feat-search', live ? 'preview' : 'building']}
              inks={[undefined, undefined, undefined, INK.violet]}
              stagger={14}
            />
          </div>
        </Fit>
        <Fit className={s.rowNarrow}>
          <div className="housing">
            <Flaps
              size="xs"
              columns={[{ width: 12 }, { width: COLS.status }]}
              values={['feat-search', live ? 'preview' : 'building']}
              inks={[undefined, INK.violet]}
              stagger={10}
            />
          </div>
        </Fit>
      </div>
    </section>
  )
}
