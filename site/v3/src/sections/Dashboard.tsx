import { useEffect, useState, type CSSProperties } from 'react'
import { Heading } from '../components/Heading'
import { Logo } from '../components/Logo'
import { motionOn, useInView } from '../lib/motion'
import { useScramble, useTyped } from '../lib/scramble'
import s from './Dashboard.module.css'

const rows = [
  { id: 'c19e77', branch: 'feat-search', msg: 'Search results page', build: '11.2s', when: '6 min ago', preview: true },
  { id: '8f3a2c', branch: 'main', msg: 'Add search page', build: '9.8s', when: '2 hours ago' },
  { id: '41d0e9', branch: 'main', msg: 'Fix RSS dates', build: '9.1s', when: 'Yesterday' },
  { id: '7a2b10', branch: 'main', msg: 'Bump astro to 4.16', build: '10.4s', when: '3 days ago' },
]

const commands = [
  ['pier rollback blog', 'Point production back at the previous deployment.'],
  ['pier promote c19e77', 'Make a preview deployment the production one.'],
  ['pier logs blog --follow', 'Stream logs from whatever production serves.'],
  ['pier env set API_KEY --preview', 'Add an encrypted variable only previews can read.'],
  ['pier domains add blog example.com', 'Attach your own domain. HTTPS follows on the first visit.'],
  ['pier board', 'Every address on your server, and what each one serves.'],
]

function Command({ cmd, text, play, delay }: { cmd: string; text: string; play: boolean; delay: number }) {
  const typed = useTyped(cmd, play, { delay })
  return (
    <li className={s.cmd}>
      <code aria-label={cmd}>
        <span aria-hidden="true">{typed}</span>
        {typed.length < cmd.length && play && <span className={s.caret} aria-hidden="true" />}
      </code>
      <p>{text}</p>
    </li>
  )
}

export function Dashboard() {
  const win = useInView<HTMLDivElement>({ margin: '0px 0px -30% 0px' })
  const grid = useInView<HTMLUListElement>()
  const [rolled, setRolled] = useState(!motionOn())

  // The dashboard replays the rollback: production slides down one row.
  useEffect(() => {
    if (!win.seen || rolled) return
    const t = setTimeout(() => setRolled(true), 700)
    return () => clearTimeout(t)
  }, [win.seen, rolled])

  const serving = useScramble(rolled ? '41d0e9' : '8f3a2c')
  const prodRow = rolled ? 2 : 1

  return (
    <section className={s.section} aria-labelledby="dash-title">
      <div className={s.head}>
        <Heading id="dash-title" text="Click it or script it." className={s.title} />
        <p className={s.lede}>
          The dashboard and the pier command both talk to the same API on your server, so anything you can click, you can
          also script.
        </p>
      </div>

      <div ref={win.ref} className={s.window} role="img" aria-label="The Pier dashboard for blog. Production serves 41d0e9 after a rollback; the feat-search preview serves c19e77.">
        <div className={s.bar} aria-hidden="true">
          <div className={s.crumbs}>
            <Logo size={18} />
            <span className={s.slash}>/</span>
            <span>blog</span>
          </div>
          <div className={s.tabs}>
            <span className={s.tabOn}>Deployments</span>
            <span>Functions</span>
            <span>Environment</span>
            <span>Domains</span>
            <span>Logs</span>
          </div>
        </div>
        <div className={s.aliases} aria-hidden="true">
          <div className={`${s.alias} ${s.prod}`}>
            <div className={s.aliasTop}>
              <span className={s.aliasName}>
                <i className={s.swatchPink} />
                Production
              </span>
              <span className={s.meta}>{rolled ? 'Rolled back just now' : 'Deployed 2 hours ago'}</span>
            </div>
            <code className={s.host}>blog.pier.example.com</code>
            <div className={s.aliasFoot}>
              <span className={s.meta}>
                Serving <code className={s.id}>{serving}</code>
              </span>
              <span className={s.btn}>{rolled ? 'Promote 8f3a2c again' : 'Roll back'}</span>
            </div>
          </div>
          <div className={`${s.alias} ${s.prev}`}>
            <div className={s.aliasTop}>
              <span className={s.aliasName}>
                <i className={s.swatchViolet} />
                Preview
              </span>
              <span className={s.meta}>Ready 6 minutes ago</span>
            </div>
            <code className={s.host}>feat-search--blog.pier.example.com</code>
            <div className={s.aliasFoot}>
              <span className={s.meta}>
                Serving <code className={s.id}>c19e77</code>
              </span>
              <span className={`${s.btn} ${s.btnOn}`}>Promote to production</span>
            </div>
          </div>
        </div>
        <div className={s.table} aria-hidden="true" style={{ '--row': prodRow } as CSSProperties}>
          <div className={`${s.tr} ${s.th}`}>
            <span>Deployment</span>
            <span>Branch</span>
            <span>Commit message</span>
            <span className={s.optional}>Build</span>
            <span className={s.optional}>Created</span>
            <span>Serving</span>
          </div>
          {rows.map((r) => (
            <div key={r.id} className={s.tr}>
              <code>{r.id}</code>
              <code className={s.branch}>{r.branch}</code>
              <span className={s.msg}>{r.msg}</span>
              <code className={`${s.dimText} ${s.optional}`}>{r.build}</code>
              <span className={`${s.dimText} ${s.optional}`}>{r.when}</span>
              <span>{r.preview ? <span className={s.badgePreview}>Preview</span> : null}</span>
            </div>
          ))}
          <span className={s.badgeProd}>Production</span>
        </div>
      </div>

      <ul ref={grid.ref} className={s.commands}>
        {commands.map(([c, t], i) => (
          <Command key={c} cmd={c} text={t} play={grid.seen} delay={i * 240} />
        ))}
      </ul>
    </section>
  )
}
