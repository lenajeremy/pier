import { useEffect, useRef, useState } from 'react'
import { LogoMark, CloseIcon, MenuIcon } from './icons'
import s from './Nav.module.css'

const links = [
  { href: '#how-it-works', label: 'How it works' },
  { href: '#features', label: 'Features' },
  { href: '#architecture', label: 'Architecture' },
  { href: '#compare', label: 'Compare' },
  { href: '#faq', label: 'FAQ' },
]

export function Logo() {
  return (
    <a href="#top" className={s.logo} aria-label="Pier, back to top">
      <LogoMark />
      <span>pier</span>
    </a>
  )
}

export function Nav() {
  const [open, setOpen] = useState(false)
  const button = useRef<HTMLButtonElement>(null)

  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        setOpen(false)
        button.current?.focus()
      }
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [open])

  return (
    <header className={s.nav} id="top">
      <Logo />
      <nav aria-label="Main" className={s.links}>
        {links.map((l) => (
          <a key={l.href} href={l.href} className={s.link}>
            {l.label}
          </a>
        ))}
      </nav>
      <a href="#join" className={s.cta}>
        Join the waitlist
      </a>
      <button
        ref={button}
        type="button"
        className={s.menuButton}
        aria-expanded={open}
        aria-controls="mobile-menu"
        aria-label={open ? 'Close menu' : 'Open menu'}
        onClick={() => setOpen((o) => !o)}
      >
        {open ? <CloseIcon size={18} /> : <MenuIcon size={18} />}
      </button>
      <div id="mobile-menu" className={s.sheet} hidden={!open}>
        <nav aria-label="Main">
          {links.map((l) => (
            <a key={l.href} href={l.href} className={s.sheetLink} onClick={() => setOpen(false)}>
              {l.label}
            </a>
          ))}
        </nav>
        <a href="#join" className={s.sheetCta} onClick={() => setOpen(false)}>
          Join the waitlist
        </a>
      </div>
    </header>
  )
}
