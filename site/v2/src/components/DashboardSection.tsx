import type { CSSProperties, ReactNode } from 'react'
import { d, InView } from '../lib/inView'
import { Cell, Cells } from './Cells'
import { ArrowUpIcon, ChevronDownIcon, LogoMark, RollbackIcon } from './icons'
import { SectionHead } from './SectionHead'
import s from './DashboardSection.module.css'

// The mock dashboard. Structure after Cofounder's "Launches" page (page
// title with counts, Preview and Live side by side with an inner hostname
// card, a release timeline) and Laravel Cloud's compact deployment rows.

type Event = {
  kind: 'build' | 'alias'
  title: string
  sub: string
  url: string
  build: string
  by: string
  when: string
  serving?: 'production' | 'preview'
}

const timeline: Event[] = [
  { kind: 'build', title: 'Search results page', sub: 'feat-search at c19e77', url: 'blog-c19e77.pier.example.com', build: '11.2s', by: 'SK', when: '3 min ago', serving: 'preview' },
  { kind: 'alias', title: 'Production now serves 8f3a2c', sub: 'main moved forward', url: 'blog.pier.example.com', build: '0.3s', by: 'SK', when: '2 hours ago' },
  { kind: 'build', title: 'Add search page', sub: 'main at 8f3a2c', url: 'blog-8f3a2c.pier.example.com', build: '9.8s', by: 'SK', when: '2 hours ago', serving: 'production' },
  { kind: 'build', title: 'Fix RSS dates', sub: 'main at 41d0e9', url: 'blog-41d0e9.pier.example.com', build: '9.1s', by: 'JO', when: 'Yesterday' },
]

const commands = [
  { cmd: 'pier projects', text: 'List your projects and what each production URL is serving.', phone: false },
  { cmd: 'pier logs blog --follow', text: "Stream runtime logs from the deployment that's live.", phone: true },
  { cmd: 'pier rollback blog', text: 'Point production back at the previous deployment.', phone: true },
  { cmd: 'pier promote c19e77', text: 'Make a preview deployment the production one.', phone: true },
  { cmd: 'pier env set API_KEY --preview', text: 'Add an encrypted variable that only previews can read.', phone: true },
  { cmd: 'pier domains add blog example.com', text: 'Attach your own domain. HTTPS follows on the first visit.', phone: false },
]

export function Badge({ alias }: { alias: 'production' | 'preview' }) {
  return <span className={`${s.badge} ${alias === 'production' ? s.production : s.preview}`}>{alias === 'production' ? 'Production' : 'Preview'}</span>
}

export function WindowBar({ compact }: { compact?: boolean }) {
  return (
    <div className={s.bar}>
      <div className={s.crumbs}>
        <LogoMark size={compact ? 16 : 18} />
        <span className={s.slash}>/</span>
        <span className={s.project}>blog</span>
        {!compact && <ChevronDownIcon size={12} className={s.chev} />}
      </div>
      {compact ? (
        <span className={s.tabActive}>Deployments</span>
      ) : (
        <div className={s.tabs}>
          <span className={s.tabActive}>Deployments</span>
          <span className={s.tab}>Functions</span>
          <span className={s.tab}>Environment</span>
          <span className={s.tab}>Domains</span>
          <span className={s.tab}>Logs</span>
        </div>
      )}
    </div>
  )
}

function Button({ children, primary }: { children: ReactNode; primary?: boolean }) {
  return <span className={`${s.btn} ${primary ? s.btnPrimary : ''}`}>{children}</span>
}

// One alias, Cofounder style: title and who sees it, an inner card with the
// hostname, then a footer line with the action.
export function AliasPanel({ kind, compact }: { kind: 'production' | 'preview'; compact?: boolean }) {
  const prod = kind === 'production'
  return (
    <div className={s.panel}>
      <div className={s.panelHead}>
        <div className={s.panelTitle}>
          <span className={s.panelName}>
            <span className={`${s.swatch} ${prod ? s.swatchProd : s.swatchPrev}`} aria-hidden="true" />
            {prod ? 'Production' : 'Preview'}
          </span>
          <span className={s.panelWho}>{prod ? 'Live to visitors' : 'Only people with the link'}</span>
        </div>
        <span className={s.chip}>{prod ? 'Up to date with main' : '1 commit ahead'}</span>
      </div>
      <div className={s.card}>
        <code className={s.host}>{prod ? 'blog.pier.example.com' : 'feat-search--blog.pier.example.com'}</code>
        <span className={s.cardText}>{prod ? 'Visitors see this deployment.' : 'Changes here are not visible to visitors.'}</span>
        <code className={s.cardUrl}>{prod ? 'blog-8f3a2c.pier.example.com' : 'blog-c19e77.pier.example.com'}</code>
      </div>
      <div className={s.panelFoot}>
        <span className={s.footText}>
          {prod ? (
            compact ? (
              'Serving 8f3a2c'
            ) : (
              <>
                Serving <code>8f3a2c</code> from main, 2 hours ago.
              </>
            )
          ) : compact ? (
            'Serving c19e77'
          ) : (
            <>
              <code>feat-search</code> has changes production doesn't.
            </>
          )}
        </span>
        {prod ? <Button>Roll back</Button> : <Button primary>{compact ? 'Promote' : 'Promote to production'}</Button>}
      </div>
    </div>
  )
}

function TypeCell({ kind }: { kind: Event['kind'] }) {
  return (
    <span className={s.type}>
      <span className={s.typeIcon}>{kind === 'build' ? <ArrowUpIcon size={13} /> : <RollbackIcon size={13} />}</span>
      <span className={s.typeChip}>{kind === 'build' ? 'Build' : 'Alias'}</span>
    </span>
  )
}

// Rows arrive one after another once the window has risen.
const rowDelay = (i: number) => ({ '--d': `${500 + i * 90}ms` }) as CSSProperties

export function DashboardSection() {
  return (
    <section className={s.section} aria-labelledby="dashboard">
      <SectionHead
        id="dashboard"
        title="A dashboard and a CLI, on one API"
        lede="pierd serves a single HTTP API. The dashboard and the pier command both talk to it, so anything you can click, you can also script."
      />

      <InView
        className={s.stage}
        role="img"
        aria-label="The Pier dashboard for the blog project. Production, blog.pier.example.com, serves deployment 8f3a2c and is up to date with main. The feat-search preview serves c19e77 and is one commit ahead. The timeline lists recent builds and the last alias change."
      >
        <div className={`${s.window} ${s.wide} m-rise`} style={d(0, { '--dur': '900ms' })} aria-hidden="true">
          <WindowBar />
          <div className={s.pageHead}>
            <div>
              <p className={s.pageTitle}>Deployments</p>
              <p className={s.pageSub}>Every push and every alias change, newest first.</p>
            </div>
            <p className={s.counts}>12 deployments, 1 preview, 1 in production</p>
          </div>
          <div className={`${s.panels} m-fade`} style={d(300)}>
            <AliasPanel kind="production" />
            <AliasPanel kind="preview" />
          </div>
          <div className={s.timelineHead}>
            <span className={s.timelineTitle}>Timeline</span>
            <span className={s.timelineSub}>Pushes and alias changes appear here as they happen.</span>
          </div>
          <div className={s.table}>
            <div className={`${s.tr} ${s.th}`}>
              <span>Type</span>
              <span>Change</span>
              <span>URL</span>
              <span>Took</span>
              <span>By</span>
              <span>When</span>
            </div>
            {timeline.map((e, i) => (
              <div key={e.title} className={`${s.tr} m-rise`} style={rowDelay(i)}>
                <TypeCell kind={e.kind} />
                <span className={s.change}>
                  <span className={s.changeTitle}>
                    {e.title}
                    {e.serving && <Badge alias={e.serving} />}
                  </span>
                  <code className={s.changeSub}>{e.sub}</code>
                </span>
                <code className={s.urlCell}>{e.url}</code>
                <span className={s.dim}>{e.build}</span>
                <span className={s.avatar}>{e.by}</span>
                <span className={s.dim}>{e.when}</span>
              </div>
            ))}
          </div>
        </div>

        <div className={`${s.window} ${s.compact} m-rise`} style={d(0, { '--dur': '900ms' })} aria-hidden="true">
          <WindowBar compact />
          <div className={s.pageHead}>
            <div>
              <p className={s.pageTitle}>Deployments</p>
              <p className={s.pageSub}>12 deployments, 1 preview, 1 in production</p>
            </div>
          </div>
          <AliasPanel kind="production" compact />
          <AliasPanel kind="preview" compact />
          {timeline.slice(1, 4).map((e, i) => (
            <div key={e.title} className={`${s.compactRow} m-rise`} style={rowDelay(i + 1)}>
              <span className={s.typeIcon}>{e.kind === 'build' ? <ArrowUpIcon size={13} /> : <RollbackIcon size={13} />}</span>
              <span className={s.change}>
                <span className={s.changeTitle}>{e.title}</span>
                <code className={s.changeSub}>{e.sub}</code>
              </span>
              <span className={s.dim}>{e.when}</span>
            </div>
          ))}
        </div>
      </InView>

      <Cells cols={[1, 2, 3]} as="ul" label="pier commands" className={s.cli}>
        {commands.map((c, i) => (
          <Cell key={c.cmd} as="li" index={i % 3} className={c.phone ? s.cmd : `${s.cmd} ${s.cmdWide}`}>
            <code className={`${s.cmdName} m-type`} style={d(0, { '--steps': c.cmd.length, '--dur': `${c.cmd.length * 28}ms` })}>
              {c.cmd}
            </code>
            <p className={`${s.cmdText} m-rise`} style={d(c.cmd.length * 28)}>
              {c.text}
            </p>
          </Cell>
        ))}
      </Cells>
      {/* Giga ends its command grid by pointing at --help. */}
      <p className={s.help}>
        Run <code>pier --help</code> to see every command.
      </p>
    </section>
  )
}
