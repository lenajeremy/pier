import { useEffect, useState } from 'react'
import { Flaps } from '../flap/Flaps'
import { Fit } from '../components/Fit'
import { WaitlistForm } from '../components/WaitlistForm'
import { useInView, useMedia } from '../lib/motion'
import { useWaitlistOutcome } from '../lib/waitlist'
import { INK } from '../story'
import s from './Ending.module.css'

/** A deployment-style ID for the visitor's row, so joining "deploys" them. */
function shortId(text: string) {
  let h = 2166136261
  for (const c of text) h = Math.imul(h ^ c.charCodeAt(0), 16777619)
  return (h >>> 0).toString(16).padStart(8, '0').slice(0, 6)
}

/** The board comes back with one empty row. As you type your email, the row
 * flips to your own domain; when you join, it gets an ID and says JOINED. */
export function Ending() {
  const phone = useMedia('(max-width: 899px)')
  const outcome = useWaitlistOutcome()
  const { ref, seen } = useInView<HTMLDivElement>()
  const [email, setEmail] = useState('')
  const [domain, setDomain] = useState('')

  // Flip once the typing pauses, not on every key.
  useEffect(() => {
    const t = setTimeout(() => {
      const at = email.lastIndexOf('@')
      const d = at >= 0 ? email.slice(at + 1).trim().toLowerCase() : ''
      setDomain(/^[a-z0-9-]+(\.[a-z0-9-]+)*\.[a-z]{2,}$/.test(d) ? d : '')
    }, 380)
    return () => clearTimeout(t)
  }, [email])

  // Phones drop the deployment column and the pier. prefix to keep tiles readable.
  const address = domain ? (phone ? domain : `pier.${domain}`) : outcome ? (phone ? 'your.server' : 'your.server.here') : ''
  const id = outcome ? shortId(email || 'pier') : ''
  const status = outcome === 'joined' ? 'joined' : outcome === 'already' ? 'listed' : 'next'
  const addrCols = phone ? 11 : 23
  const columns = phone ? [{ width: addrCols }, { width: 7 }] : [{ width: addrCols }, { width: 6 }, { width: 7 }]
  const pick = (a: string, d: string, st: string) => (phone ? [a, st] : [a, d, st])

  const rows: { a: string; d: string; st: string; ink: string }[] = [
    { a: phone ? 'blog' : 'blog.pier.example.com', d: '8f3a2c', st: 'live', ink: INK.pink },
    { a: phone ? 'status' : 'status.pier.example.com', d: '3d9c04', st: 'healthy', ink: INK.teal },
  ]

  return (
    <section id="join" className={s.section} aria-labelledby="join-title">
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
              values={pick(address.slice(0, addrCols), id, status)}
              inks={phone ? [undefined, outcome ? INK.teal : undefined] : [undefined, undefined, outcome ? INK.teal : undefined]}
              play={seen}
              delay={300}
              stagger={22}
              label={outcome ? `Your row: ${status}` : 'An empty row, next on the board'}
            />
          </div>
        </Fit>
      </div>
      <h2 id="join-title" className={s.title}>
        Your server is next on the board.
      </h2>
      <p className={s.lede}>One email when the first release is ready to install. Nothing else.</p>
      <WaitlistForm className={s.form} onEmail={setEmail} />
    </section>
  )
}
