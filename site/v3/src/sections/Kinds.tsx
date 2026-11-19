import type { ReactNode } from 'react'
import { Flaps } from '../flap/Flaps'
import { Figure } from '../components/Figure'
import { Heading } from '../components/Heading'
import { useInView } from '../lib/motion'
import { INK } from '../story'
import s from './Kinds.module.css'

function Card({
  figure,
  status,
  ink,
  title,
  text,
  children,
}: {
  figure: 'frontends' | 'functions' | 'services'
  status: string
  ink: string
  title: string
  text: string
  children: ReactNode
}) {
  const { ref, seen } = useInView<HTMLLIElement>()
  return (
    <li ref={ref} className={s.card}>
      <Figure kind={figure} className={s.figure} />
      <Flaps size="sm" columns={[{ width: 7 }]} values={[status]} inks={[ink]} play={seen} delay={300} stagger={50} label={`Status ${status}`} />
      <div className={s.text}>
        <h3 className={s.cardTitle}>{title}</h3>
        <p className={s.cardText}>{text}</p>
      </div>
      <div className={s.panel}>{children}</div>
    </li>
  )
}

export function Kinds() {
  return (
    <section className={s.section} aria-labelledby="kinds-title">
      <div className={s.head}>
        <Heading id="kinds-title" text="Three kinds of app, one board." className={s.title} />
        <p className={s.lede}>Pier looks at what you pushed and runs each part the right way. The status column tells you which.</p>
      </div>
      <ul className={s.cards}>
        <Card figure="frontends" status="live" ink={INK.pink} title="Frontends" text="Built from whatever you pushed, then served as plain static files by Caddy.">
          <div className={s.chips}>
            {['Vite', 'Astro', 'Next.js export', 'HTML'].map((c) => (
              <span key={c}>{c}</span>
            ))}
          </div>
          <p className={s.mono}>
            blog-8f3a2c.pier.example.com
            <br />
            214 files, 1.3 MB, served by Caddy
          </p>
        </Card>
        <Card
          figure="functions"
          status="asleep"
          ink={INK.asleep}
          title="Functions"
          text="Files in api/ answer at /api/*. Their container sleeps when nobody calls and wakes on the next request."
        >
          <p className={s.mono}>
            <span className={s.dim}>14:02:19 </span> api/og.go asleep after 5m idle
          </p>
          <p className={s.mono}>
            <span className={s.dim}>14:03:02 </span> GET /api/og?slug=search
          </p>
          <p className={`${s.mono} ${s.teal}`}>
            <span className={s.dim}>14:03:02 </span> woke in 160ms, 200 OK
          </p>
        </Card>
        <Card
          figure="services"
          status="healthy"
          ink={INK.teal}
          title="Services"
          text="A Dockerfile at the root runs for good, with health checks, restarts after a crash and updates with no downtime."
        >
          <dl className={s.facts}>
            <div>
              <dt>Health check</dt>
              <dd>GET /healthz every 10s</dd>
            </div>
            <div>
              <dt>Last answer</dt>
              <dd className={s.teal}>200 in 4ms</dd>
            </div>
            <div>
              <dt>Restarts this week</dt>
              <dd>0</dd>
            </div>
          </dl>
        </Card>
      </ul>
    </section>
  )
}
