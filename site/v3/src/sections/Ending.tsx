import { useState } from 'react'
import { Flaps } from '../flap/Flaps'
import { Fit } from '../components/Fit'
import { InstallCommand, commands, origin } from '../components/InstallCommand'
import { useInView, useMedia } from '../lib/motion'
import { INK } from '../story'
import s from './Ending.module.css'

/** A deployment-style ID for the visitor's row. */
function shortId(text: string) {
  let h = 2166136261
  for (const c of text) h = Math.imul(h ^ c.charCodeAt(0), 16777619)
  return (h >>> 0).toString(16).padStart(8, '0').slice(0, 6)
}

/** The board comes back with one empty row, next in line. Copying the server
 * command gives it an ID and flips it to COPIED: the visitor's server is the
 * next thing on the board. */
export function Ending() {
  const phone = useMedia('(max-width: 899px)')
  const { ref, seen } = useInView<HTMLDivElement>()
  const [copied, setCopied] = useState(false)

  // Phones drop the deployment column to keep tiles readable.
  const address = phone ? 'your.server' : 'your.server.here'
  const id = copied ? shortId(origin) : ''
  const status = copied ? 'copied' : 'next'
  const addrCols = phone ? 11 : 23
  const columns = phone ? [{ width: addrCols }, { width: 7 }] : [{ width: addrCols }, { width: 6 }, { width: 7 }]
  const pick = (a: string, d: string, st: string) => (phone ? [a, st] : [a, d, st])

  const rows: { a: string; d: string; st: string; ink: string }[] = [
    { a: phone ? 'blog' : 'blog.pier.example.com', d: '8f3a2c', st: 'live', ink: INK.pink },
    { a: phone ? 'status' : 'status.pier.example.com', d: '3d9c04', st: 'healthy', ink: INK.teal },
  ]

  return (
    <section id="install" className={s.section} aria-labelledby="install-title">
      <div ref={ref} className={s.board}>
        <Fit>
          <div className="housing" key={phone ? 'p' : 'd'}>
            {rows.map((r, i) => (
              <Flaps
                key={r.a}
                size={phone ? 'xs' : 'lg'}
                columns={columns}
                values={pick(r.a, r.d, r.st)}
                inks={phone ? [undefined, r.ink] : [undefined, undefined, r.ink]}
                play={seen}
                delay={i * 140}
                stagger={14}
              />
            ))}
            <Flaps
              size={phone ? 'xs' : 'lg'}
              columns={columns}
              values={pick(address, id, status)}
              inks={phone ? [undefined, copied ? INK.teal : undefined] : [undefined, undefined, copied ? INK.teal : undefined]}
              play={seen}
              delay={300}
              stagger={22}
              label={copied ? 'Your server, next on the board' : 'An empty row, next on the board'}
            />
          </div>
        </Fit>
      </div>
      <h2 id="install-title" className={s.title}>
        Your server is next on the board.
      </h2>
      <p className={s.lede}>Install Pier on your server, then deploy from your computer.</p>
      <ol className={s.steps}>
        <li>
          <span className={s.step}>
            On your server <span>Linux, with Docker</span>
          </span>
          <InstallCommand cmd={commands.server} label="Install Pier on your server" onCopy={() => setCopied(true)} />
        </li>
        <li>
          <span className={s.step}>
            On your computer <span>macOS or Linux</span>
          </span>
          <InstallCommand cmd={commands.cli} label="Install the pier command on your computer" />
        </li>
      </ol>
      <p className={s.after}>
        Then run <code>pier deploy</code> in a project, or connect GitHub from the dashboard.{' '}
        <a href="https://github.com/lenajeremy/pier/releases">Release notes</a>
      </p>
    </section>
  )
}
