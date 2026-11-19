import { Fragment, useState } from 'react'
import { d, InView } from '../lib/inView'
import { site } from '../site'
import { Canvas } from './Canvas'
import { CheckIcon, ChevronDownIcon, LogoMark } from './icons'
import s from './Compare.module.css'

type Value = true | false | string

const tools = ['Pier', 'Vercel', 'Coolify', 'Dokku'] as const

// Cloudflare's plan cards, above the table: what each tool is and where it
// runs (their "price" line).
const cards = [
  { name: 'Pier', text: 'Your own deploy platform, on one server.', runsOn: 'Your server' },
  { name: 'Vercel', text: 'A hosted frontend cloud.', runsOn: 'Their cloud' },
  { name: 'Coolify', text: 'A self-hosted platform with a web UI.', runsOn: 'Your server or theirs' },
  { name: 'Dokku', text: 'A Heroku-style platform you drive from the CLI.', runsOn: 'Your server' },
]

// Non-Pier cells come from each project's public docs as of November 2026.
// Re-check them before launch. `why` is Framer's "what this means for you".
type Row = { label: string; why: string; values: [Value, Value, Value, Value] }
const groups: { name: string; open: boolean; rows: Row[] }[] = [
  {
    name: 'Where it runs',
    open: true,
    rows: [
      { label: 'Runs on your own server', why: 'Your code, data and logs stay on a machine you control.', values: [true, false, true, true] },
      { label: 'What you pay for', why: 'The bill that arrives each month.', values: ['Your server', 'Seats and usage', 'Your server or theirs', 'Your server'] },
      { label: 'A web dashboard', why: 'See and change deployments without a terminal.', values: [true, true, true, 'CLI only'] },
    ],
  },
  {
    name: 'Deploying',
    open: true,
    rows: [
      { label: 'Deploy with git push', why: 'No pipeline to set up for a plain deploy.', values: [true, true, true, true] },
      { label: 'A preview URL for every branch', why: "Share work in progress before it's merged.", values: [true, true, 'For pull requests', 'Not built in'] },
      { label: 'Roll back without rebuilding', why: 'A bad deploy is undone in seconds.', values: [true, true, true, 'By hand'] },
    ],
  },
  {
    name: 'Running',
    open: false,
    rows: [
      { label: 'Functions that sleep when idle', why: "Small APIs don't hold memory while nobody calls them.", values: [true, true, false, false] },
      { label: 'Long-running Docker services', why: 'Workers, bots and anything else with a Dockerfile.', values: [true, false, true, true] },
      { label: 'Custom domains with automatic HTTPS', why: 'Certificates are issued and renewed for you.', values: [true, true, true, 'With a plugin'] },
      { label: 'Build and runtime logs', why: 'Every deployment keeps what it printed.', values: [true, true, true, true] },
    ],
  },
]

// Shorter text for the phone layout, where each cell is a quarter of 350px.
const short: Record<string, string> = {
  'For pull requests': 'For PRs',
  'Seats and usage': 'Seats, usage',
  'Your server or theirs': 'Server or cloud',
  'With a plugin': 'Plugin',
}

function Mark({ value, compact }: { value: Value; compact?: boolean }) {
  if (value === true) return <CheckIcon size={compact ? 15 : 16} aria-label="Yes" role="img" aria-hidden={undefined} />
  if (value === false) return <span className={s.dash} role="img" aria-label="No" />
  return <span className={s.word}>{compact ? (short[value] ?? value) : value}</span>
}

export function Compare() {
  const [open, setOpen] = useState(() => groups.map((g) => g.open))
  const allOpen = open.every(Boolean)
  let rowIndex = 0

  return (
    <section className={s.section} aria-labelledby="compare">
      <InView className={s.head}>
        <h2 id="compare" className={`${s.title} m-rise`}>
          How Pier compares
        </h2>
        <p className={`${s.lede} m-rise`} style={d(100)}>
          The workflow of a hosted platform, on a server you control. Here's how it lines up with the tools people ask
          about most.
        </p>
      </InView>

      <InView className={s.wrap} style={{ ['--stagger' as string]: '50ms' }}>
        <Canvas className={s.canvas}>
          <div className={s.cards}>
            <div className={s.cardsIntro}>
              <p className={s.introTitle}>Four ways to deploy from git</p>
              <p className={s.introText}>Same push-to-deploy idea, four places it can run.</p>
            </div>
            {cards.map((c, i) => (
              <div key={c.name} className={`${s.card} ${i === 0 ? s.cardPier : ''} m-rise`} style={d(120, { '--i': i, '--stagger': '80ms' })}>
                <p className={s.cardName}>
                  {i === 0 && <LogoMark size={15} />}
                  {c.name}
                </p>
                <p className={s.cardText}>{c.text}</p>
                <p className={s.runsLabel}>Runs on</p>
                <p className={s.runsOn}>{c.runsOn}</p>
                {i === 0 && (
                  <a href="#join" className={s.cardCta}>
                    Join the waitlist
                  </a>
                )}
              </div>
            ))}
          </div>

          <div className={s.tableBox}>
            <div className={s.tableBar}>
              <span>Compare features</span>
              <button type="button" className={s.expand} onClick={() => setOpen(groups.map(() => !allOpen))}>
                {allOpen ? 'Collapse all' : 'Expand all'}
              </button>
            </div>
            <table className={s.table}>
              <caption className="visually-hidden">Pier compared with Vercel, Coolify and Dokku</caption>
              <thead>
                <tr>
                  <td />
                  {tools.map((t, i) => (
                    <th key={t} scope="col" className={i === 0 ? s.pierCol : undefined}>
                      {t}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {groups.map((g, gi) => (
                  <Fragment key={g.name}>
                    <tr className={s.groupRow}>
                      <th colSpan={5} scope="colgroup">
                        <button
                          type="button"
                          className={s.groupButton}
                          aria-expanded={open[gi]}
                          onClick={() => setOpen((o) => o.map((v, j) => (j === gi ? !v : v)))}
                        >
                          {g.name}
                          <ChevronDownIcon size={14} className={`${s.chev} ${open[gi] ? s.chevOpen : ''}`} />
                        </button>
                      </th>
                    </tr>
                    {g.rows.map((r) => {
                      const ri = rowIndex++
                      return (
                        <tr key={r.label} hidden={!open[gi]} className="m-fade" style={d(200, { '--i': ri })}>
                          <th scope="row">
                            <span className={s.rowLabel}>{r.label}</span>
                            <span className={s.rowWhy}>{r.why}</span>
                          </th>
                          {r.values.map((v, i) => (
                            <td key={tools[i]} className={i === 0 ? s.pierCol : undefined}>
                              <Mark value={v} />
                            </td>
                          ))}
                        </tr>
                      )
                    })}
                  </Fragment>
                ))}
              </tbody>
            </table>
          </div>
        </Canvas>
      </InView>

      {/* Phones: one row per feature, the four answers under it. */}
      <InView className={s.stack} role="table" aria-label="Pier compared with Vercel, Coolify and Dokku">
        <div className={s.stackHead} role="row">
          {tools.map((t, i) => (
            <span key={t} role="columnheader" className={i === 0 ? s.pierCell : undefined}>
              {t}
            </span>
          ))}
        </div>
        {groups.map((g) => (
          <Fragment key={g.name}>
            <div className={s.stackGroup} role="row">
              <span role="rowheader">{g.name}</span>
            </div>
            {g.rows.map((r, ri) => (
              <div key={r.label} className={`${s.stackRow} m-rise`} style={d(100, { '--i': ri, '--stagger': '70ms' })} role="row">
                <span className={s.stackLabel} role="rowheader">
                  {r.label}
                </span>
                <div className={s.stackCells}>
                  {r.values.map((v, i) => (
                    <span key={tools[i]} role="cell" className={i === 0 ? s.pierCell : undefined}>
                      <Mark value={v} compact />
                    </span>
                  ))}
                </div>
              </div>
            ))}
          </Fragment>
        ))}
      </InView>

      <p className={s.note}>
        Based on each project's public docs, November 2026. If something here is out of date,{' '}
        <a href={site.contact} className={s.noteLink}>
          tell us
        </a>{' '}
        and we'll fix it.
      </p>
    </section>
  )
}
