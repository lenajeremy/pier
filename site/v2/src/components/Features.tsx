import type { ReactNode } from 'react'
import { d } from '../lib/inView'
import { Cell, Cells } from './Cells'
import {
  FrontendIcon,
  FunctionIcon,
  GlobeIcon,
  HeartbeatIcon,
  LayersIcon,
  ListIcon,
  LockIcon,
  MoonIcon,
  RefreshIcon,
  ServiceIcon,
  TerminalIcon,
} from './icons'
import { SectionHead } from './SectionHead'
import s from './Features.module.css'

type Spec = [icon: ReactNode, text: ReactNode]

// A tile after Railway's primitive cards: icon, title, one-line promise, a
// small piece of real UI (Replit), then the concrete specifics, one per row.
function Tile({
  index,
  icon,
  title,
  text,
  children,
  specs,
  small,
}: {
  index: number
  icon: ReactNode
  title: string
  text: string
  children: ReactNode
  specs: Spec[]
  small?: boolean
}) {
  return (
    <Cell as="li" index={index} className={small ? s.tileSmall : s.tile}>
      <div className={s.tileHead}>
        <span className={`${s.iconBox} m-pop`}>{icon}</span>
        <h3 className={`${small ? s.titleSmall : s.title} m-rise`} style={d(60)}>
          {title}
        </h3>
      </div>
      <p className={`${s.text} m-rise`} style={d(120)}>
        {text}
      </p>
      <div className={`${s.demo} m-rise`} style={d(200)}>
        {children}
      </div>
      <ul className={s.specs}>
        {specs.map(([ic, t], i) => (
          <li key={i} className={`${s.spec} m-rise`} style={d(320, { '--i': i, '--stagger': '60ms' })}>
            <span className={s.specIcon}>{ic}</span>
            <span>{t}</span>
          </li>
        ))}
      </ul>
    </Cell>
  )
}

function Row({ name, children, mono = true }: { name: string; children: ReactNode; mono?: boolean }) {
  return (
    <div className={s.row}>
      {mono ? <code className={s.rowName}>{name}</code> : <span className={s.rowLabel}>{name}</span>}
      {children}
    </div>
  )
}

function Dot({ live, children }: { live?: boolean; children: ReactNode }) {
  return (
    <span className={s.state}>
      <span className={live ? s.dotLive : s.dotIdle} aria-hidden="true" />
      {children}
    </span>
  )
}

// A function that sleeps and wakes on a loop. `awake` is its resting state
// without motion; `phase` shifts it within the loop so rows take turns.
function Sleeper({ awake, phase, wakeLabel }: { awake?: boolean; phase: string; wakeLabel: string }) {
  return (
    <span className={`${s.state} ${s.sleeper} ${awake ? s.isAwake : s.isAsleep}`} style={{ ['--phase' as string]: phase }}>
      <span className={`${s.dotIdle} ${s.sleepDot} m-loop`} aria-hidden="true" />
      <span className={s.swap}>
        <span className={`${s.asleepLabel} m-loop`}>Asleep</span>
        <span className={`${s.awakeLabel} m-loop`}>{wakeLabel}</span>
      </span>
    </span>
  )
}

const logLines = [
  ['14:02:11', 'GET /api/og?slug=search 200 41ms'],
  ['14:02:12', 'GET /api/subscribe 405 2ms'],
  ['14:02:19', 'api/og.go asleep after 5m idle'],
  ['14:03:02', 'api/search.py woke in 160ms'],
  ['14:03:02', 'GET /api/search?q=rss 200 18ms'],
  ['14:03:40', 'GET /feed.xml 200 3ms'],
]

const I = 15

export function Features() {
  return (
    <section className={s.section} aria-labelledby="features">
      <SectionHead
        id="features"
        title="Three kinds of app, one way to ship"
        lede="Pier looks at what you pushed and runs each part the right way, on the same machine and behind the same domain."
      />

      <Cells cols={[1, 2, 3]} as="ul" stagger={110} className={s.cells}>
        <Tile
          index={0}
          icon={<FrontendIcon size={18} />}
          title="Frontends"
          text="Built in a throwaway container from whatever you pushed, then served as plain static files."
          specs={[
            [<LayersIcon size={I} />, 'Vite, Astro or a Next.js static export'],
            [<FrontendIcon size={I} />, 'Plain HTML, with no build step'],
            [<GlobeIcon size={I} />, 'Its own URL for every deployment'],
          ]}
        >
          <div className={s.mini}>
            <div className={s.chips}>
              {['Vite', 'Astro', 'Next.js export', 'HTML'].map((f, i) => (
                <span key={f} className={`${s.chip} m-pop`} style={d(300, { '--i': i, '--stagger': '70ms' })}>
                  {f}
                </span>
              ))}
            </div>
            <span className={s.buildTrack} aria-hidden="true">
              <span className={`${s.buildFill} m-grow`} style={d(650, { '--dur': '1200ms' })} />
            </span>
            <code className={`${s.buildLine} m-type`} style={d(1850, { '--steps': 32, '--dur': '640ms' })}>
              built in 9.8s, 214 files, 1.3 MB
            </code>
          </div>
        </Tile>

        <Tile
          index={1}
          icon={<FunctionIcon size={18} />}
          title="Functions"
          text="Files in api/ answer at /api/*. They run in a container that sleeps when nobody calls it and wakes on the next request."
          specs={[
            [<FunctionIcon size={I} />, 'JavaScript, TypeScript, Go and Python'],
            [<ServiceIcon size={I} />, 'One container for each deployment'],
            [<MoonIcon size={I} />, 'Asleep after 5 idle minutes'],
          ]}
        >
          <div className={s.list}>
            <Row name="api/subscribe.ts">
              <Sleeper phase="1.6s" wakeLabel="Awake, 9 ms" />
            </Row>
            <Row name="api/og.go">
              <Sleeper awake phase="-2.2s" wakeLabel="Awake, 12 ms" />
            </Row>
            <Row name="api/search.py">
              <Dot>Asleep</Dot>
            </Row>
          </div>
        </Tile>

        <Tile
          index={2}
          icon={<ServiceIcon size={18} />}
          title="Services"
          text="A Dockerfile at the root runs as a long-lived service, with health checks, zero-downtime updates and restarts after a crash."
          specs={[
            [<ServiceIcon size={I} />, 'Any Dockerfile at the root of the repo'],
            [<HeartbeatIcon size={I} />, 'Health-checked before traffic moves'],
            [<RefreshIcon size={I} />, 'Restarted after a crash'],
          ]}
        >
          <div className={s.list}>
            <Row name="status.pier.example.com">
              <span className={s.healthy}>
                <span className={`${s.beat} m-loop`} aria-hidden="true" />
                Healthy
              </span>
            </Row>
            <Row name="Health check" mono={false}>
              <code className={s.value}>GET /healthz every 10s</code>
            </Row>
            <Row name="Restarts this week" mono={false}>
              <code className={s.value}>0</code>
            </Row>
          </div>
        </Tile>

        {/* Railway's strip between the two rows of primitives. */}
        <Cell as="li" className={s.strip}>
          <p className="m-fade">
            Pier reads <code>package.json</code>, <code>api/</code> and your <code>Dockerfile</code> to work out how to
            build and run each part. There's no config file to write.
          </p>
        </Cell>

        <Tile
          index={0}
          small
          icon={<LockIcon size={18} />}
          title="Environment variables"
          text="Stored encrypted, and scoped to production or to previews."
          specs={[
            [<LockIcon size={I} />, 'Encrypted on your own server'],
            [<LayersIcon size={I} />, 'Production, preview or both'],
            [
              <TerminalIcon size={I} />,
              <>
                <code>pier env set</code> from the CLI
              </>,
            ],
          ]}
        >
          <div className={`${s.list} ${s.plain}`}>
            <Row name="DATABASE_URL">
              <span className={s.secret}>
                <span className={s.mask} aria-label="hidden value">
                  ••••••••
                </span>
                <span className={s.scope}>Production</span>
              </span>
            </Row>
            <Row name="RESEND_API_KEY">
              <span className={s.secret}>
                <span className={s.mask} aria-label="hidden value">
                  ••••••••
                </span>
                <span className={s.scope}>Preview</span>
              </span>
            </Row>
          </div>
        </Tile>

        <Tile
          index={1}
          small
          icon={<GlobeIcon size={18} />}
          title="Custom domains with HTTPS"
          text="Point a domain at your server. Caddy gets the certificate on the first request."
          specs={[
            [<GlobeIcon size={I} />, 'Any domain or subdomain you own'],
            [<LockIcon size={I} />, 'Certificates issued and renewed by Caddy'],
          ]}
        >
          {/* Replit's "domain linking" card, shrunk. */}
          <div className={s.addDomain} aria-hidden="true">
            <span className={s.addField}>docs.example.com</span>
            <span className={s.addButton}>Add domain</span>
          </div>
          <div className={`${s.list} ${s.plain}`}>
            <Row name="blog.example.com">
              <Dot live>Certificate issued</Dot>
            </Row>
            <Row name="docs.example.com">
              <span className={`${s.state} ${s.cert}`}>
                <span className={`${s.dotIdle} ${s.certDot} m-loop`} aria-hidden="true" />
                <span className={s.swap}>
                  <span className={`${s.certWaiting} m-loop`}>Waiting for DNS</span>
                  <span className={`${s.certIssuing} m-loop`} aria-hidden="true">
                    Getting certificate
                  </span>
                  <span className={`${s.certIssued} m-loop`} aria-hidden="true">
                    Certificate issued
                  </span>
                </span>
              </span>
            </Row>
          </div>
        </Tile>

        <Tile
          index={2}
          small
          icon={<ListIcon size={18} />}
          title="Build and runtime logs"
          text="Every deployment keeps its logs. Read them in the dashboard or follow them from the CLI."
          specs={[
            [<ListIcon size={I} />, 'Build logs kept with each deployment'],
            [
              <TerminalIcon size={I} />,
              <>
                <code>pier logs --follow</code> for live output
              </>,
            ],
          ]}
        >
          <pre className={s.logs}>
            {/* Lines scroll up like pier logs --follow. The last three repeat
                the first three so the loop is seamless. */}
            <code className={s.window}>
              <span className={`${s.tail} m-loop`}>
                {[...logLines, ...logLines.slice(0, 3)].map(([time, msg], i) => (
                  <span key={i} className={s.logLine} aria-hidden={i >= 3 ? true : undefined}>
                    <span className={s.time}>{time}</span> {msg}
                  </span>
                ))}
              </span>
            </code>
          </pre>
        </Tile>
      </Cells>
    </section>
  )
}
