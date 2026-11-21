import { Fragment } from 'react'
import { Flaps } from '../flap/Flaps'
import { Heading } from '../components/Heading'
import { useInView } from '../lib/motion'
import s from './Compare.module.css'

type Cell = 'yes' | 'no' | string
type Row = { feature: string; note: string; cells: [Cell, Cell, Cell, Cell] }

const groups: { name: string; rows: Row[] }[] = [
  {
    name: 'Where it runs',
    rows: [
      { feature: 'Runs on your own server', note: 'Your code, data and logs stay on a machine you control.', cells: ['yes', 'no', 'yes', 'yes'] },
      { feature: 'What you pay for', note: 'The bill that arrives each month.', cells: ['Your server', 'Seats and usage', 'Your server or theirs', 'Your server'] },
      { feature: 'A web dashboard', note: 'See and change deployments without a terminal.', cells: ['yes', 'yes', 'yes', 'CLI only'] },
    ],
  },
  {
    name: 'Deploying',
    rows: [
      { feature: 'Deploy with git push', note: 'No pipeline to set up for a plain deploy.', cells: ['yes', 'yes', 'yes', 'yes'] },
      { feature: 'A preview URL for every branch', note: "Share work in progress before it's merged.", cells: ['yes', 'yes', 'For pull requests', 'Not built in'] },
      { feature: 'Roll back without rebuilding', note: 'A bad deploy is undone in seconds.', cells: ['yes', 'yes', 'yes', 'By hand'] },
    ],
  },
  {
    name: 'Running',
    rows: [
      { feature: 'Previews that sleep when idle', note: "A preview's server holds no memory while nobody visits.", cells: ['yes', 'yes', 'no', 'no'] },
      { feature: 'Long-running Docker services', note: 'Workers, bots and anything with a Dockerfile.', cells: ['yes', 'no', 'yes', 'yes'] },
      { feature: 'Custom domains with HTTPS', note: 'Certificates issued and renewed for you.', cells: ['yes', 'yes', 'yes', 'With a plugin'] },
    ],
  },
]

const names = ['Pier', 'Vercel', 'Coolify', 'Dokku']

function Value({ v, pier, i }: { v: Cell; pier: boolean; i: number }) {
  if (v === 'yes')
    return (
      <svg className={s.check} viewBox="0 0 16 16" fill="none" aria-label="Yes" role="img" style={{ '--d': `${i * 45}ms` } as React.CSSProperties}>
        <path d="M3 8.5L6.5 12L13 4.5" pathLength={1} stroke={pier ? 'var(--flap-ink)' : 'var(--muted)'} strokeWidth={1.6} strokeLinecap="round" strokeLinejoin="round" />
      </svg>
    )
  if (v === 'no') return <span className={s.no} aria-label="No" role="img" />
  return <span className={pier ? s.textPier : s.text}>{v}</span>
}

export function Compare() {
  const { ref, seen } = useInView<HTMLDivElement>()
  let n = 0
  return (
    <section id="compare" className={s.section} aria-labelledby="compare-title">
      <div className={s.head}>
        <Heading id="compare-title" text="How Pier compares." className={s.title} />
        <p className={s.lede}>The workflow of a hosted platform, on a server you control. Checked against each project's docs in November 2026.</p>
      </div>
      <div ref={ref} className={s.table} data-seen={seen ? '' : undefined} role="table" aria-label="Pier compared with Vercel, Coolify and Dokku">
        <div className={`${s.row} ${s.header}`} role="row">
          <span className={s.corner} role="columnheader">
            <span className="visually-hidden">Feature</span>
          </span>
          <span className={`${s.cell} ${s.pier} ${s.pierHead}`} role="columnheader" aria-label="Pier">
            <Flaps size="sm" columns={[{ width: 4 }]} values={['pier']} play={seen} delay={200} stagger={70} label="Pier" />
          </span>
          <span className={s.cell} role="columnheader">
            <img src="/logos/vercel.svg" alt="" width={16} height={16} />
            Vercel
          </span>
          <span className={s.cell} role="columnheader">
            <img src="/logos/coolify.svg" alt="" width={16} height={16} />
            Coolify
          </span>
          <span className={s.cell} role="columnheader">
            <img src="/logos/dokku.png" alt="" width={22} height={22} />
            Dokku
          </span>
        </div>
        {groups.map((g) => (
          <Fragment key={g.name}>
            <div className={s.group} role="row">
              <span role="rowheader">{g.name}</span>
            </div>
            {g.rows.map((r) => {
              const k = n++
              return (
                <div key={r.feature} className={s.row} role="row">
                  <span className={s.feature} role="rowheader">
                    <span className={s.featureName}>{r.feature}</span>
                    <span className={s.note}>{r.note}</span>
                  </span>
                  {r.cells.map((c, i) => (
                    <span key={i} className={`${s.cell} ${i === 0 ? s.pier : ''}`} role="cell" data-name={names[i]}>
                      <Value v={c} pier={i === 0} i={k} />
                    </span>
                  ))}
                </div>
              )
            })}
          </Fragment>
        ))}
      </div>
    </section>
  )
}
