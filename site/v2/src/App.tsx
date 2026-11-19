import { Architecture } from './components/Architecture'
import { Compare } from './components/Compare'
import { DashboardSection } from './components/DashboardSection'
import { Faq } from './components/Faq'
import { Features } from './components/Features'
import { Footer } from './components/Footer'
import { Hero } from './components/Hero'
import { HowItWorks } from './components/HowItWorks'
import { Nav } from './components/Nav'
import { WaitlistBand } from './components/WaitlistBand'
import s from './App.module.css'

// The page is a white column on a dotted sheet. The waitlist band is the one
// thing that runs the full width of the window, so the column is split
// around it.
export function App() {
  return (
    <div className={s.page}>
      <a className="skip-link" href="#main">
        Skip to content
      </a>
      <div className={s.column}>
        <Nav />
      </div>
      <main id="main">
        <div className={s.column}>
          <Hero />
          <HowItWorks />
          <Features />
          <DashboardSection />
          <Architecture />
          <Compare />
          <Faq />
        </div>
        <WaitlistBand />
      </main>
      <div className={s.column}>
        <Footer />
      </div>
    </div>
  )
}
