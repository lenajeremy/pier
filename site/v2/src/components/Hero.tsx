import { HeroFlowTall, HeroFlowWide } from '../diagrams/HeroFlow'
import { d, InView } from '../lib/inView'
import { Canvas } from './Canvas'
import { WaitlistForm } from './WaitlistForm'
import s from './Hero.module.css'

export function Hero() {
  return (
    <section className={s.hero} aria-labelledby="hero-title">
      <InView className={s.header}>
        <h1 id="hero-title" className={s.title}>
          <span className="m-rise">Your own deploy platform,</span>{' '}
          <span className="m-rise" style={d(90)}>
            on the server you already have
          </span>
        </h1>
        <p className={`${s.lede} m-rise`} style={d(200)}>
          Push static sites, functions and Docker services to one machine. Every branch gets a preview URL
          <span className={s.long}> and every deployment is kept, so a rollback takes a second.</span>
          <span className={s.short}>, and a rollback takes a second.</span>
        </p>
        <div className={`${s.form} m-rise`} style={d(300)}>
          <WaitlistForm />
        </div>
      </InView>

      <InView live threshold={0.15} className={`${s.stage} m-fade`} style={d(250)}>
        <Canvas handles="top" className={s.canvas}>
          <HeroFlowWide className={s.wide} />
          <HeroFlowTall className={s.tall} />
          <div className={`${s.caption} m-rise`} style={d(1300)}>
            <p className={s.captionLead}>Every branch you push gets its own address with HTTPS as soon as the build finishes.</p>
            <p className={s.captionNote}>
              Merge it, and production moves to the new deployment.
              <span className={s.long}> The preview stays up until you delete it.</span>
            </p>
          </div>
        </Canvas>
      </InView>
    </section>
  )
}
