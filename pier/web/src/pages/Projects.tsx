import type { ProjectSummary, Session } from '../lib/api'
import { ago, short } from '../lib/format'
import { Link } from '../lib/router'
import { Branch, Check } from '../components/Icons'
import { ProjectMark } from '../components/ProjectMark'
import { statusWord } from '../components/ui'
import { StatusFlaps } from '../flap/Flaps'

// Projects is the home: every project with what it's serving, or, before
// the first deploy, the getting-started checklist (B2).
export function Projects({ session, projects, now }: { session: Session; projects: ProjectSummary[]; now: number }) {
  if (projects.length === 0) return <GettingStarted session={session} />
  return (
    <main className="main global">
      <div className="page-heading">
        <div>
          <h1 className="t-title" style={{ margin: 0 }}>
            Projects
          </h1>
          <span className="t-caption t-muted">
            {projects.length === 1 ? 'One project' : `${projects.length} projects`} on {session.server.address}
          </span>
        </div>
        <Link to="/new" className="btn primary">
          New project
        </Link>
      </div>
      <div className="panel">
        {projects.map((p) => (
          <ProjectRow key={p.name} p={p} now={now} />
        ))}
      </div>
    </main>
  )
}

function ProjectRow({ p, now }: { p: ProjectSummary; now: number }) {
  // What the project serves, or its newest build if nothing is live yet.
  const d = p.production ?? p.latest
  return (
    <Link to={`/projects/${p.name}`} className="project-row">
      <ProjectMark key={p.name} name={p.name} size={36} />
      <div className="who">
        <span className="t-small t-strong">{p.name}</span>
        <span className="mono t-caption t-muted">{p.address}</span>
      </div>
      <div className="what">
        {d ? (
          <>
            <span className="t-small">{d.message || short(d.commit)}</span>
            <span className="meta">
              <span className="branch mono t-caption">
                <Branch size={13} color="#8A92A8" />
                {d.branch}
              </span>
              <span className="t-note">{ago(d.created_at, now)}</span>
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
    </Link>
  )
}

function GettingStarted({ session }: { session: Session }) {
  const { address, base_domain: base } = session.server
  return (
    <main className="main global" style={{ paddingTop: 36, gap: 20 }}>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
        <h1 className="t-title" style={{ margin: 0 }}>
          Welcome to Pier
        </h1>
        <span className="t-caption t-muted">Nothing is deployed yet. Two steps to your first live project.</span>
      </div>
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
          <Link to="/new" className="btn quiet">
            New project
          </Link>
        </div>
      </div>
    </main>
  )
}
