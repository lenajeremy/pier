import { Logo } from './Nav'
import { GithubIcon, LogoMark } from './icons'
import { InView } from '../lib/inView'
import { site } from '../site'
import s from './Footer.module.css'

type Link = { label: string; href: string; wide?: boolean; soon?: boolean }

// Columns after Dovetail (short lists, a small chip where a page isn't live
// yet) and Retool (calls to action and a giant mark as the sign-off).
const columns: { title: string; links: Link[] }[] = [
  {
    title: 'Product',
    links: [
      { label: 'How it works', href: '#how-it-works' },
      { label: 'Features', href: '#features' },
      { label: 'Architecture', href: '#architecture', wide: true },
      { label: 'Compare', href: '#compare' },
      { label: 'FAQ', href: '#faq', wide: true },
    ],
  },
  {
    title: 'Project',
    links: [
      { label: 'GitHub', href: site.github, soon: true },
      { label: 'Changelog', href: site.changelog, soon: true },
      { label: 'Docs', href: site.docs, soon: true },
    ],
  },
  {
    title: 'About',
    links: [
      { label: 'Contact', href: site.contact },
    ],
  },
]

export function Footer() {
  return (
    <footer className={s.footer}>
      <div className={s.top}>
        <div className={s.brand}>
          <Logo />
          <p className={s.tagline}>Git push deploys, branch previews and rollbacks on a server you own.</p>
          <div className={s.actions}>
            <a href="#join" className={s.cta}>
              Join the waitlist
            </a>
            <a href={site.github} className={s.iconLink} aria-label="Pier on GitHub, coming soon">
              <GithubIcon size={16} />
            </a>
          </div>
        </div>
        <nav aria-label="Footer" className={s.columns}>
          {columns.map((col) => (
            <div key={col.title} className={s.column}>
              <h2 className={s.colTitle}>{col.title}</h2>
              <ul>
                {col.links.map((l) => (
                  <li key={l.label} className={l.wide ? s.wideOnly : undefined}>
                    <a href={l.href} className={s.link}>
                      {l.label}
                      {l.soon && <span className={s.soon}>Soon</span>}
                    </a>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </nav>
      </div>
      <div className={s.legal}>
        <p>© 2026 Pier</p>
        <div className={s.legalLinks}>
          <a href={site.privacy} className={s.legalLink}>
            Privacy
          </a>
          <span>Made for one server, on purpose.</span>
        </div>
      </div>
      <InView className={s.wordmark} rootMargin="0px" aria-hidden="true">
        <span className={s.word}>
          <LogoMark className={s.mark} />
          pier
        </span>
      </InView>
    </footer>
  )
}
