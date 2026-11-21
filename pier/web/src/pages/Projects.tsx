import { useMemo, useState } from 'react'
import type { ProjectSummary, Session } from '../lib/api'
import { short } from '../lib/format'
import { Link, navigate } from '../lib/router'
import { Branch, Check, Copy, Rows, Settings, Terminal, Visit } from '../components/Icons'
import { Menu } from '../components/Menu'
import { ProjectMark } from '../components/ProjectMark'
import { copy } from '../components/Toast'
import { PageHeading, Paging, SearchField, statusWord, usePaged, When } from '../components/ui'
import { StatusFlaps } from '../flap/Flaps'

const PAGE = 20

// Projects is the home: every project with what it's serving, or, before
// the first deploy, the getting-started checklist (B2).
export function Projects({ session, projects, now }: { session: Session; projects: ProjectSummary[]; now: number }) {
  const [query, setQuery] = useState('')
  const shown = useMemo(() => {
    const q = query.trim().toLowerCase()
    return projects.filter((p) => !q || p.name.includes(q) || p.address.includes(q))
  }, [projects, query])
  const paged = usePaged(shown, PAGE, query)

  if (projects.length === 0) return <GettingStarted session={session} />
  return (
    <main className="main global">
      <PageHeading title="Projects" note={`${projects.length === 1 ? 'One project' : `${projects.length} projects`} on ${session.server.address}`}>
        {projects.length > 6 && <SearchField value={query} onChange={setQuery} placeholder="Search projects" hotkey="/" />}
        <Link to="/new" className="btn primary">
          New project
        </Link>
      </PageHeading>
      <div className="stack">
        <div className="panel">
          {paged.shown.map((p) => (
            <ProjectRow key={p.name} p={p} now={now} />
          ))}
          {shown.length === 0 && (
            <div className="t-empty">
              <span className="t-caption t-muted">No project matches “{query.trim()}”.</span>
              <button className="btn quiet small" onClick={() => setQuery('')}>
                Show all
              </button>
            </div>
          )}
        </div>
        <Paging paged={paged} noun="project" />
      </div>
    </main>
  )
}

function ProjectRow({ p, now }: { p: ProjectSummary; now: number }) {
  // What the project serves, or its newest build if nothing is live yet.
  const d = p.production ?? p.latest
  const page = `/projects/${p.name}`
  return (
    <div className="project-row" onClick={() => navigate(page)}>
      <ProjectMark key={p.name} name={p.name} size={36} />
      <div className="who">
        <Link to={page} className="t-small t-strong" onClick={(e) => e.stopPropagation()}>
          {p.name}
        </Link>
        <span className="mono t-caption t-muted">{p.address}</span>
      </div>
      <div className="what">
        {d ? (
          <>
            <span className="t-small">{d.message || (d.commit ? short(d.commit) : 'No commit message')}</span>
            <span className="meta">
              <span className="branch mono t-caption">
                {d.source === 'cli' ? <Terminal size={13} color="#8A92A8" /> : <Branch size={13} color="#8A92A8" />}
                {d.branch || 'pier deploy'}
              </span>
              <When at={d.created_at} now={now} className="t-note" />
            </span>
          </>
        ) : (
          <span className="t-small t-muted">Nothing deployed yet</span>
        )}
      </div>
      <span className="status">
        {p.status === 'none' ? (
          <span className="t-caption t-muted">{statusWord.none}</span>
        ) : (
          <StatusFlaps status={p.status} word={statusWord[p.status]} />
        )}
      </span>
      <span className="row-more" onClick={(e) => e.stopPropagation()}>
        <Menu
          label={`Actions for ${p.name}`}
          className="icon-btn bare"
          items={[
            ...(p.production ? [{ label: 'Visit', icon: <Visit color="#A3ABBE" />, href: p.url }] : []),
            { label: 'Copy address', icon: <Copy />, onSelect: () => copy(p.url) },
            { label: 'Logs', icon: <Rows />, to: page + '/logs' },
            { label: 'Settings', icon: <Settings size={16} color="#A3ABBE" />, to: page + '/settings' },
          ]}
        />
      </span>
    </div>
  )
}

function GettingStarted({ session }: { session: Session }) {
  const { address, base_domain: base } = session.server
  return (
    <main className="main global">
      <PageHeading title="Welcome to Pier" note="Nothing is deployed yet. Two steps to your first live project." />
      <div className="panel">
        <div className="checklist-row done">
          <img src="/steps/1.png" alt="1" />
          <div>
            <span className="t-small t-strong">Set up the server</span>
            <span className="t-caption t-muted">
              {base
                ? `${address} is live, and projects get addresses under ${base}.`
                : `Pier is at ${address}. Projects need a base domain for their addresses.`}
            </span>
          </div>
          <span className="checklist-done">
            <Check />
            Done
          </span>
        </div>
        <div className="checklist-row">
          <img src="/steps/2.png" alt="2" />
          <div>
            <span className="t-small t-strong">Create your first project</span>
            <span className="t-caption t-muted">
              Import a GitHub repository or deploy a folder from your computer with pier deploy.
            </span>
          </div>
          <Link to="/new" className="btn primary">
            New project
          </Link>
        </div>
      </div>
    </main>
  )
}
