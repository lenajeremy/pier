import { useState } from 'react'
import { ServerDiagramTall, ServerDiagramWide, type ServerPart } from '../diagrams/ServerDiagram'
import { d, InView } from '../lib/inView'
import { Canvas } from './Canvas'
import { ChevronDownIcon } from './icons'
import { SectionHead } from './SectionHead'
import s from './Architecture.module.css'

const parts = [
  {
    name: 'pierd',
    role: 'The daemon',
    part: 'pierd',
    node: 'pierd',
    fact: 'One Go binary',
    text: 'The daemon on your server. It takes pushes, runs builds, keeps every deployment in SQLite, and serves the API and the dashboard.',
  },
  {
    name: 'Caddy',
    role: 'Routing and HTTPS',
    part: 'caddy',
    node: 'caddy',
    fact: 'Admin API on localhost:2019',
    text: 'Routes every hostname to the right deployment. pierd configures it through its admin API, and it gets a certificate the first time a new domain is visited.',
  },
  {
    name: 'Docker',
    role: 'Builds and containers',
    part: 'docker',
    node: 'docker',
    fact: 'One container per deployment',
    text: 'Each build runs in a throwaway container. Functions get a container that sleeps when idle, and services run long-lived with health checks.',
  },
  {
    name: 'dashboard',
    role: 'Built into pierd',
    part: 'pierd',
    node: 'pierd',
    fact: 'Served by pierd itself',
    text: "A React app embedded in pierd, so there's nothing extra to host. It uses the same HTTP API as the CLI.",
  },
  {
    name: 'pier',
    role: 'CLI on your laptop',
    part: 'git',
    node: 'laptop',
    fact: 'Uses the same HTTP API',
    text: 'The command line tool for logs, rollbacks, variables and domains. Deploys themselves are a plain git push.',
  },
] as const satisfies readonly { name: string; role: string; part: string; node: ServerPart; fact: string; text: string }[]

export function Architecture() {
  const [open, setOpen] = useState(0)
  // The diagram stays whole until someone picks a part from the list.
  const [picked, setPicked] = useState(false)
  const focus = picked ? parts[open].node : undefined

  return (
    <section className={s.section} aria-labelledby="architecture">
      <SectionHead
        id="architecture"
        title="Small enough to understand"
        lede="Pier is one Go daemon and one CLI, built on Caddy and Docker. There's no cluster to run and no database server to look after."
      />

      <InView live className={s.body}>
        <ul className={s.list}>
          {parts.map((p, i) => {
            const isOpen = open === i
            return (
              <li
                key={p.name}
                className={`${isOpen ? `${s.item} ${s.open} ${s[`edge_${p.part}`]}` : s.item} m-rise`}
                style={d(150, { '--i': i, '--stagger': '70ms' })}
              >
                <button
                  type="button"
                  className={s.trigger}
                  aria-expanded={isOpen}
                  aria-controls={`part-${i}`}
                  onClick={() => {
                    setOpen(i)
                    setPicked(true)
                  }}
                >
                  <span className={s.swatch + ' ' + s[p.part]} aria-hidden="true" />
                  <span className={s.label}>
                    <code className={s.name}>{p.name}</code>
                    <span className={s.role}>{p.role}</span>
                  </span>
                  <ChevronDownIcon size={14} className={`${s.chev} ${isOpen ? s.chevOpen : ''}`} />
                </button>
                <div id={`part-${i}`} className={s.panel} data-open={isOpen ? '' : undefined}>
                  <div className={s.panelInner}>
                    <p className={s.text}>{p.text}</p>
                  </div>
                </div>
              </li>
            )
          })}
        </ul>

        <Canvas className={s.diagram}>
          {/* Sunday's panel header: what's selected, and one fact about it. */}
          <div className={s.panelBar} aria-live="polite">
            <code className={s.panelName}>{parts[open].name}</code>
            <span className={s.panelFact}>{parts[open].fact}</span>
          </div>
          <ServerDiagramWide className={s.wide} focus={focus} />
          <ServerDiagramTall className={s.tall} focus={focus} />
        </Canvas>
      </InView>
    </section>
  )
}
