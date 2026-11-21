import { useEffect, useRef, useState } from 'react'
import { Flaps } from '../flap/Flaps'
import { gsap, motionOn, useInView, useMedia } from '../lib/motion'
import { InstallCommand, commands } from '../components/InstallCommand'
import { Logo } from '../components/Logo'
import { Fit } from '../components/Fit'
import { board, COLS, INK, type BoardRow } from '../story'
import s from './Hero.module.css'

const links = [
  ['How it works', '#push'],
  ['Rollbacks', '#rollback'],
  ['Architecture', '#architecture'],
  ['FAQ', '#faq'],
] as const

export function Nav() {
  return (
    <header className={s.nav}>
      <a href="#top" className={s.logo} aria-label="Pier, back to top">
        <Logo />
        <span>pier</span>
      </a>
      <nav aria-label="Main" className={s.links}>
        {links.map(([l, h]) => (
          <a key={h} href={h}>
            {l}
          </a>
        ))}
      </nav>
      <a href="#install" className={s.cta}>
        Install
      </a>
    </header>
  )
}

/** The hero board: every address on the server, and what it serves. It boots
 * row by row, then keeps working while it's on screen: a build finishes, a
 * preview wakes for a request and goes back to sleep. */
function LiveBoard({ phone }: { phone: boolean }) {
  const [rows, setRows] = useState<BoardRow[]>(board)
  const [booted, setBooted] = useState(false)
  const { ref, visible } = useInView<HTMLDivElement>({ live: true, margin: '0px' })
  const tilt = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const t = setTimeout(() => setBooted(true), motionOn() ? 2600 : 0)
    return () => clearTimeout(t)
  }, [])

  // The board's own events, only while it can be seen.
  useEffect(() => {
    if (!booted || !visible || !motionOn()) return
    const patch = (i: number, p: Partial<BoardRow>) => setRows((r) => r.map((row, k) => (k === i ? { ...row, ...p } : row)))
    const timers = [
      setTimeout(() => patch(1, { status: 'preview' }), 1400),
      setTimeout(() => patch(5, { status: 'awake', ink: INK.teal }), 4200),
      setTimeout(() => patch(5, { status: 'asleep', ink: INK.asleep }), 9800),
      setTimeout(() => patch(4, { status: 'live' }), 12500),
    ]
    return () => timers.forEach(clearTimeout)
  }, [booted, visible])

  // A slight 3D tilt that follows the pointer.
  useEffect(() => {
    const el = tilt.current
    if (!el || !motionOn() || phone) return
    gsap.set(el, { transformPerspective: 2200, rotationX: 16, rotationY: 0 })
    const rx = gsap.quickTo(el, 'rotationX', { duration: 0.9, ease: 'power3.out' })
    const ry = gsap.quickTo(el, 'rotationY', { duration: 0.9, ease: 'power3.out' })
    const move = (e: PointerEvent) => {
      const x = e.clientX / window.innerWidth - 0.5
      const y = e.clientY / window.innerHeight - 0.5
      rx(16 - y * 6)
      ry(x * 5)
    }
    window.addEventListener('pointermove', move)
    return () => window.removeEventListener('pointermove', move)
  }, [phone])

  // Label widths follow the tiles: n tiles plus the gap tile after a column.
  const [w, tg] = phone ? [15, 2] : [25, 3]
  const span = (n: number, last: boolean) => n * (w + tg) - tg + (last ? 0 : 2 * tg + w * 0.55)
  const labels: [string, number][] = phone
    ? [['Address', 12], ['Status', 8]]
    : [['Address', COLS.address], ['Deployment', COLS.deployment], ['Branch', COLS.branch], ['Status', COLS.status]]

  return (
    <div ref={ref} className={s.boardStage}>
      <Fit max={phone ? 1 : 1.3}>
      <div ref={tilt} className={`${s.board} housing`} style={{ transform: phone ? undefined : 'perspective(2200px) rotateX(16deg)' }}>
        <div className={s.labels} aria-hidden="true">
          {labels.map(([t, n], i) => (
            <span key={t} style={{ width: span(n, i === labels.length - 1) }}>
              {t}
            </span>
          ))}
        </div>
        <div role="list" aria-label="Every address on the server and what it serves" className={s.rows}>
          {rows.map((r, i) => (
            <div role="listitem" key={r.address}>
            {phone ? (
              <Flaps
                size="xs"
                columns={[{ width: 12 }, { width: COLS.status }]}
                values={[r.sub, r.status]}
                inks={[undefined, r.ink]}
                delay={500 + i * 90}
                stagger={10}
                label={`${r.address} serves ${r.deployment} from ${r.branch}, ${r.status}`}
              />
            ) : (
              <Flaps
                size="md"
                columns={[{ width: COLS.address }, { width: COLS.deployment }, { width: COLS.branch }, { width: COLS.status }]}
                values={[r.address, r.deployment, r.branch, r.status]}
                inks={[undefined, undefined, undefined, r.ink]}
                delay={500 + i * 90}
                stagger={12}
                label={`${r.address} serves ${r.deployment} from ${r.branch}, ${r.status}`}
              />
            )}
            </div>
          ))}
        </div>
      </div>
      </Fit>
    </div>
  )
}

export function Hero() {
  const phone = useMedia('(max-width: 899px)')
  return (
    <section className={s.hero} id="top" aria-labelledby="hero-title">
      <Nav />
      <h1 id="hero-title" className="visually-hidden">
        Ship to your own server
      </h1>
      <div className={`${s.headline} housing`} aria-hidden="true">
        {phone ? (
          <>
            <Flaps size="lg" columns={[{ width: 8 }]} values={['SHIP TO']} delay={250} stagger={40} />
            <Flaps size="lg" columns={[{ width: 8 }]} values={['YOUR OWN']} delay={420} stagger={40} />
            <Flaps size="lg" columns={[{ width: 8 }]} values={['SERVER']} delay={590} stagger={40} />
          </>
        ) : (
          <Flaps size="xl" columns={[{ width: 23 }]} values={['SHIP TO YOUR OWN SERVER']} delay={250} stagger={34} />
        )}
      </div>
      <div className={s.intro}>
        <p>
          Pier is a deploy platform for the server you already have. Every git push gets its own address, and the board
          flips the moment it changes.
        </p>
        <InstallCommand cmd={commands.server} label="Install Pier on your server" className={s.form} />
        <p className={s.note}>
          Run it on a Linux server with Docker. It sets up Pier, then asks you to create the owner's account.{' '}
          <a href="https://github.com/lenajeremy/pier/tree/main/pier#readme">Read the guide</a>
        </p>
      </div>
      <LiveBoard key={phone ? 'p' : 'd'} phone={phone} />
    </section>
  )
}
