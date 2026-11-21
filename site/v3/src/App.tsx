import { useEffect } from 'react'
import { ScrollTrigger, startSmoothScroll } from './lib/motion'
import { Hero } from './sections/Hero'
import { Push } from './sections/Push'
import { Kinds } from './sections/Kinds'
import { Rollback } from './sections/Rollback'
import { Dashboard } from './sections/Dashboard'
import { Architecture } from './sections/Architecture'
import { Compare } from './sections/Compare'
import { Faq } from './sections/Faq'
import { Ending } from './sections/Ending'
import { Footer } from './sections/Footer'

export function App() {
  useEffect(() => {
    const lenis = startSmoothScroll()
    // In-page links go through Lenis so pinned scenes stay in step.
    const onClick = (e: MouseEvent) => {
      const a = (e.target as HTMLElement).closest('a[href^="#"]') as HTMLAnchorElement | null
      if (!a || !lenis) return
      const id = a.getAttribute('href')!
      const el = id === '#top' ? document.body : document.querySelector(id)
      if (!el) return
      e.preventDefault()
      lenis.scrollTo(el as HTMLElement, { offset: 0 })
      history.replaceState(null, '', id)
    }
    document.addEventListener('click', onClick)
    // Pins are measured once the fonts have set the real line heights.
    document.fonts?.ready.then(() => ScrollTrigger.refresh())
    return () => {
      document.removeEventListener('click', onClick)
      lenis?.destroy()
    }
  }, [])

  return (
    <>
      <a className="skip-link" href="#install">
        Skip to installing Pier
      </a>
      <Hero />
      <main>
        <Push />
        <Kinds />
        <Rollback />
        <Dashboard />
        <Architecture />
        <Compare />
        <Faq />
        <Ending />
      </main>
      <Footer />
    </>
  )
}
