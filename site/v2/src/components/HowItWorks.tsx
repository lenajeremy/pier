import type { CSSProperties } from 'react'
import { AliasDiagramTall, AliasDiagramWide } from '../diagrams/AliasDiagram'
import { d, InView } from '../lib/inView'
import { Canvas } from './Canvas'
import { PushLog } from './PushLog'
import { SectionHead } from './SectionHead'
import s from './HowItWorks.module.css'

// The rollback diagram and its terminal share one loop.
const rollbackLoop = { '--cycle': '8s', '--start': '1.4s' } as CSSProperties

export function HowItWorks() {
  return (
    <section className={s.section} aria-labelledby="how-it-works">
      <SectionHead
        id="how-it-works"
        title="From git push to a live URL"
        lede="Your workflow stays in git. Everything else happens on your server, in four steps you never have to run by hand."
      />

      <PushLog />

      <InView live className={s.rollback} style={rollbackLoop}>
        <div className={s.copy}>
          <h3 className={`${s.rollbackTitle} m-rise`}>Rollbacks rebuild nothing</h3>
          <p className={`${s.rollbackText} m-rise`} style={d(80)}>
            Production and every branch preview are aliases, and each one points at a single deployment. Old deployments
            stay where they are, so a rollback points production back at one of them. Nothing rebuilds.
          </p>
          <pre className={`${s.terminal} m-rise`} style={d(160)}>
            <code className={s.termA}>
              <span className={`${s.line} ${s.a1} m-loop`}>
                <span className={s.prompt}>$</span> pier rollback blog
              </span>
              <span className={`${s.line} ${s.output} ${s.a2} m-loop`}>production now serves 41d0e9 (0.3s)</span>
            </code>
            <code className={s.termB} aria-hidden="true">
              <span className={`${s.line} ${s.b1} m-loop`}>
                <span className={s.prompt}>$</span> pier promote 8f3a2c
              </span>
              <span className={`${s.line} ${s.output} ${s.b2} m-loop`}>production now serves 8f3a2c (0.2s)</span>
            </code>
          </pre>
        </div>
        <Canvas className={s.diagram}>
          <AliasDiagramWide className={s.wide} />
          <AliasDiagramTall className={s.tall} />
        </Canvas>
      </InView>
    </section>
  )
}
