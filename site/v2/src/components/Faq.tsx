import { useState } from 'react'
import { MinusIcon, PlusIcon } from './icons'
import { d, InView } from '../lib/inView'
import { site } from '../site'
import s from './Faq.module.css'

// Drafted to match how the product is described; check each answer against
// the real implementation before launch.
type Faq = { q: string; a: string; cards?: { title: string; text: string; code: string }[] }

const faqs: Faq[] = [
  {
    q: 'What do I need to run Pier?',
    a: "Three things. Pier drives Caddy for routing and certificates, so there's no proxy to set up yourself.",
    // Sui opens an answer into a row of small cards.
    cards: [
      { title: 'A Linux server', text: 'A VPS or a box at home that can reach the internet.', code: 'ssh you@203.0.113.10' },
      { title: 'Docker', text: 'Every build, function and service runs in a container.', code: 'docker version' },
      { title: 'A wildcard DNS record', text: 'So every deployment and branch gets its own name.', code: '*.pier.example.com  A  203.0.113.10' },
    ],
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
    a: 'Encrypted in pierd\'s database on your server. Each variable is available to production, to previews, or to both, and reaches builds and containers as an ordinary environment variable.',
  },
  {
    q: 'When can I use it?',
    a: "Pier is still being built. Join the waitlist and you'll get one email when the first release is ready to install. Nothing else.",
  },
]

export function Faq() {
  const [open, setOpen] = useState<Set<number>>(() => new Set([0]))

  function toggle(i: number) {
    setOpen((prev) => {
      const next = new Set(prev)
      if (next.has(i)) next.delete(i)
      else next.add(i)
      return next
    })
  }

  return (
    <section className={s.section} aria-labelledby="faq">
      <InView className={s.intro}>
        <h2 id="faq" className={`${s.title} m-rise`}>
          Questions about running Pier
        </h2>
        <p className={`${s.lede} m-rise`} style={d(80)}>
          Something we didn't cover?{' '}
          <a href={site.github} className={s.link}>
            Open an issue on GitHub
          </a>{' '}
          and we'll answer it there.
        </p>
      </InView>

      <InView as="ul" className={s.list}>
        {faqs.map((f, i) => {
          const isOpen = open.has(i)
          return (
            <li key={f.q} className={`${s.item} m-rise`} style={d(100, { '--i': i, '--stagger': '60ms' })}>
              <h3>
                <button
                  type="button"
                  className={s.question}
                  aria-expanded={isOpen}
                  aria-controls={`faq-${i}`}
                  onClick={() => toggle(i)}
                >
                  <span className={s.q}>
                    <span className={s.bullet} aria-hidden="true" />
                    {f.q}
                  </span>
                  <span className={s.toggle} data-open={isOpen ? '' : undefined} aria-hidden="true">
                    <PlusIcon size={12} strokeWidth={1.4} className={s.plus} />
                    <MinusIcon size={12} strokeWidth={1.4} className={s.minus} />
                  </span>
                </button>
              </h3>
              <div id={`faq-${i}`} className={s.panel} data-open={isOpen ? '' : undefined}>
                <div className={s.panelInner}>
                  <p className={s.answer}>{f.a}</p>
                  {f.cards && (
                    <ul className={s.cards}>
                      {f.cards.map((c) => (
                        <li key={c.title} className={s.card}>
                          <span className={s.cardTitle}>{c.title}</span>
                          <span className={s.cardText}>{c.text}</span>
                          <code className={s.cardCode}>{c.code}</code>
                        </li>
                      ))}
                    </ul>
                  )}
                </div>
              </div>
            </li>
          )
        })}
      </InView>
    </section>
  )
}
