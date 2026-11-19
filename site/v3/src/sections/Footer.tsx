import { Flaps } from '../flap/Flaps'
import { Logo } from '../components/Logo'
import { useInView } from '../lib/motion'
import s from './Footer.module.css'

const cols = [
  { title: 'Product', links: [['How it works', '#push'], ['Rollbacks', '#rollback'], ['Architecture', '#architecture'], ['Compare', '#compare']] },
  { title: 'Project', links: [['GitHub', '#'], ['Changelog', '#'], ['Docs', '#']] },
  { title: 'About', links: [['Contact', '#'], ['Privacy', '#']] },
]

export function Footer() {
  const { ref, seen } = useInView<HTMLDivElement>({ margin: '0px 0px -5% 0px' })
  return (
    <footer className={s.footer}>
      <div className={s.top}>
        <div className={s.brand}>
          <a href="#top" className={s.logo} aria-label="Pier, back to top">
            <Logo />
            <span>pier</span>
          </a>
          <p>Git push deploys, branch previews and rollbacks on a server you own.</p>
        </div>
        <nav className={s.cols} aria-label="Footer">
          {cols.map((c) => (
            <div key={c.title} className={s.col}>
              <p className={s.colTitle}>{c.title}</p>
              <ul>
                {c.links.map(([l, h]) => (
                  <li key={l}>
                    <a href={h}>{l}</a>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </nav>
      </div>
      <div className={s.legal}>
        <p>© 2026 Pier</p>
        <p>Made for one server, on purpose.</p>
      </div>
      <div ref={ref} className={s.wordmark} aria-hidden="true">
        <div className={`${s.mark} housing`}>
          <Flaps className={s.tiles} columns={[{ width: 4 }]} values={['pier']} play={seen} stagger={110} />
        </div>
      </div>
    </footer>
  )
}
