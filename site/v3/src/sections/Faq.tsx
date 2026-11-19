import { useState } from 'react'
import { Flaps } from '../flap/Flaps'
import { Heading } from '../components/Heading'
import s from './Faq.module.css'

const items = [
  {
    q: 'What do I need to run Pier?',
    a: "A Linux server with Docker, and a wildcard DNS record such as *.pier.example.com pointing at it. Pier drives Caddy for routing and certificates, so there's no proxy to set up yourself.",
  },
  {
    q: 'How close is it to Vercel?',
    a: "Close in workflow: git push deploys, a URL for every deployment and branch, functions under /api, and instant rollbacks. It isn't a global network. Everything runs on your one server, so there's no CDN, no regions and no edge middleware.",
  },
  {
    q: 'What happens to old deployments?',
    a: "They stay on disk with their own URL until you delete them, which is what makes rollbacks instant. You can delete previews older than a date, or have them removed after a merge. Production's history is never deleted for you.",
  },
  {
    q: 'Do sleeping functions use any memory?',
    a: "No. When a function hasn't been called for a while (5 minutes by default) Pier stops its container. The next request starts it again, so that one request waits while it wakes.",
  },
  {
    q: 'How are environment variables stored?',
    a: "Encrypted in pierd's database on your server. Each variable is available to production, to previews, or to both, and reaches builds and containers as an ordinary environment variable.",
  },
  {
    q: 'When can I use it?',
    a: "Pier is still being built. Join the waitlist and you'll get one email when the first release is ready to install. Nothing else.",
  },
]

/** The toggle is one tile that flips between + and −. */
export function Faq() {
  const [open, setOpen] = useState<Set<number>>(() => new Set([0]))
  const toggle = (i: number) =>
    setOpen((o) => {
      const n = new Set(o)
      if (n.has(i)) n.delete(i)
      else n.add(i)
      return n
    })

  return (
    <section id="faq" className={s.section} aria-labelledby="faq-title">
      <div className={s.inner}>
      <div className={s.intro}>
        <Heading id="faq-title" text="Questions about running Pier." className={s.title} />
        <p className={s.lede}>Something we didn't cover? Open an issue on GitHub and we'll answer it there.</p>
      </div>
      <ul className={s.list}>
        {items.map((it, i) => {
          const on = open.has(i)
          return (
            <li key={it.q} className={s.item}>
              <h3>
                <button type="button" className={s.q} aria-expanded={on} aria-controls={`faq-${i}`} id={`faq-q-${i}`} onClick={() => toggle(i)}>
                  <span>{it.q}</span>
                  <span className={s.tile} aria-hidden="true">
                    <Flaps size="sm" columns={[{ width: 1 }]} values={[on ? '-' : '+']} stagger={0} />
                  </span>
                </button>
              </h3>
              <div id={`faq-${i}`} role="region" aria-labelledby={`faq-q-${i}`} className={s.a} data-open={on ? '' : undefined}>
                <div>
                  <p>{it.a}</p>
                </div>
              </div>
            </li>
          )
        })}
      </ul>
      </div>
    </section>
  )
}
