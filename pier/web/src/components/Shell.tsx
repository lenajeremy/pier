import { useEffect, useRef, useState, type ReactNode } from 'react'
import type { ProjectSummary, Session } from '../lib/api'
import { signOut } from '../lib/api'
import { useData } from '../lib/data'
import { Link } from '../lib/router'
import { CommandMenu } from './CommandMenu'
import * as I from './Icons'
import { ProjectMark } from './ProjectMark'
import { Spinner } from './Spinner'
import type { Sample } from './ServerCharts'

const globals = [
  { to: '/', label: 'Projects', icon: I.Projects },
  { to: '/activity', label: 'Activity', icon: I.Activity },
  { to: '/integrations', label: 'Integrations', icon: I.Integrations },
  { to: '/shared-variables', label: 'Shared variables', icon: I.Braces },
  { to: '/members', label: 'Members', icon: I.Members },
  { to: '/server', label: 'Server', icon: I.Server },
  { to: '/settings', label: 'Settings', icon: I.Settings },
]

type Props = {
  session: Session
  projects: ProjectSummary[]
  path: string
  project?: string // the open project, if any
  children: ReactNode
}

// Shell is the sidebar and the content beside it: the sidebar answers
// "which project", the project's tabs answer "which part of it".
export function Shell({ session, projects, path, project, children }: Props) {
  const recent = useData<{ samples: Sample[] }>('/api/server/history?range=latest', () => true, 60000)
  const sample = recent.data?.samples.at(-1)
  const pressure = sample && (sample.disk >= 85 ? 'The disk is more than 85% full' : sample.memory >= 90 ? 'Memory is more than 90% used' : '')
  const [menuOpen, setMenuOpen] = useState(false)
  const [finding, setFinding] = useState(false)
  useEffect(() => {
    setMenuOpen(false)
    setFinding(false)
  }, [path])

  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        setFinding((f) => !f)
      }
    }
    window.addEventListener('keydown', key)
    return () => window.removeEventListener('keydown', key)
  }, [])

  const active = (to: string) => !project && (to === '/' ? path === '/' || path.startsWith('/new') : path.startsWith(to))
  const mac = /Mac|iPhone|iPad/.test(navigator.platform)

  return (
    <div className="app">
      <button
        className="mobile-nav-toggle"
        aria-expanded={menuOpen}
        aria-controls="server-navigation"
        onClick={() => setMenuOpen(!menuOpen)}
      >
        <I.Mark size={16} />
        {menuOpen ? 'Close menu' : 'Menu'}
      </button>
      <aside id="server-navigation" className={'sidebar' + (menuOpen ? ' mobile-open' : '')}>
        <Switcher session={session} projects={projects} project={project} />
        <button className="side-search" onClick={() => setFinding(true)} aria-haspopup="dialog">
          <I.Search />
          <span>Search</span>
          <span className="kbd">{mac ? '⌘K' : 'Ctrl K'}</span>
        </button>
        <nav className="nav" aria-label="Server">
          {globals.map(({ to, label, icon: Icon }) => (
            <Link key={to} to={to} className={'nav-item' + (active(to) ? ' active' : '')} aria-current={active(to) ? 'page' : undefined}>
              <Icon />
              <span className="grow">{label}</span>
              {to === '/server' && pressure && <span className="nav-alert" role="img" aria-label={pressure} data-tip={pressure} />}
            </Link>
          ))}
        </nav>
        <nav className="nav side-projects" aria-label="Projects">
          <div className="side-projects-head">
            <span className="t-note">Projects</span>
            <Link to="/new" className="icon-btn-24" aria-label="New project" data-tip="New project">
              <I.Plus />
            </Link>
          </div>
          <div className="side-projects-list">
            {projects.map((p) => (
              <Link
                key={p.name}
                to={`/projects/${p.name}`}
                className={'side-project' + (p.name === project ? ' active' : '')}
                aria-current={p.name === project ? 'page' : undefined}
              >
                <i className={'swatch ' + p.status} />
                <span>{p.name}</span>
              </Link>
            ))}
          </div>
        </nav>
        <Account session={session} />
      </aside>
      <div className="content">{children}</div>
      {finding && (
        <CommandMenu projects={projects} project={project} branch={session.server.production_branch} onClose={() => setFinding(false)} />
      )}
    </div>
  )
}

function Switcher({ session, projects, project }: { session: Session; projects: ProjectSummary[]; project?: string }) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  useClickAway(ref, () => setOpen(false))
  return (
    <div ref={ref} style={{ position: 'relative' }}>
      <button className="switcher" onClick={() => setOpen(!open)} aria-expanded={open} aria-haspopup="menu">
        {project ? (
          <ProjectMark key={project} name={project} />
        ) : (
          <span className="switcher-mark">
            <I.Mark />
          </span>
        )}
        <span className="switcher-text">
          <span className="t-caption t-strong">{project ?? 'All projects'}</span>
          <span className="t-note">{session.server.address}</span>
        </span>
        <I.UpDown />
      </button>
      {open && (
        <div className="menu switcher-menu" role="menu">
          <Link to="/" className="menu-item" role="menuitem" onClick={() => setOpen(false)}>
            <span className="switcher-mark small">
              <I.Mark size={14} />
            </span>
            <span className="grow">All projects</span>
            {!project && <I.Check size={14} color="#8DB2F8" />}
          </Link>
          {projects.length > 0 && <div className="menu-sep" />}
          {projects.map((p) => (
            <Link key={p.name} to={`/projects/${p.name}`} className="menu-item" role="menuitem" onClick={() => setOpen(false)}>
              <i className={'swatch ' + p.status} />
              <span className="grow">{p.name}</span>
              {p.name === project && <I.Check size={14} color="#8DB2F8" />}
            </Link>
          ))}
          <div className="menu-sep" />
          <Link to="/new" className="menu-item" role="menuitem" onClick={() => setOpen(false)}>
            <I.Plus size={14} />
            <span className="grow">New project</span>
          </Link>
        </div>
      )}
    </div>
  )
}

function Account({ session }: { session: Session }) {
  const [open, setOpen] = useState(false)
  const [leaving, setLeaving] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  useClickAway(ref, () => setOpen(false))
  const version = session.server.version
  const out = async () => {
    setLeaving(true)
    try {
      await signOut()
    } finally {
      window.dispatchEvent(new Event('pier:signed-out'))
    }
  }
  return (
    <div ref={ref} className="account-box">
      <button className="account" onClick={() => setOpen(!open)} aria-expanded={open} aria-haspopup="menu">
        <div>
          <span className="t-small">{session.user?.name || 'Owner'}</span>
          <span className="t-note">{session.user?.email}</span>
        </div>
        <I.UpDown size={16} />
      </button>
      {open && (
        <div className="menu account-menu" role="menu">
          <Link to="/members" className="menu-item" role="menuitem" onClick={() => setOpen(false)}>
            Where you're signed in
          </Link>
          <Link to="/settings" className="menu-item" role="menuitem" onClick={() => setOpen(false)}>
            Change password
          </Link>
          <div className="menu-sep" />
          <button className="menu-item" role="menuitem" disabled={leaving} onClick={out}>
            {leaving && <Spinner />}
            {leaving ? 'Signing out…' : 'Sign out'}
          </button>
          {version && (
            <>
              <div className="menu-sep" />
              <span className="menu-note t-note">Pier {version}</span>
            </>
          )}
        </div>
      )}
    </div>
  )
}

function useClickAway(ref: React.RefObject<HTMLElement | null>, away: () => void) {
  useEffect(() => {
    const down = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) away()
    }
    const key = (e: KeyboardEvent) => e.key === 'Escape' && away()
    document.addEventListener('mousedown', down)
    document.addEventListener('keydown', key)
    return () => {
      document.removeEventListener('mousedown', down)
      document.removeEventListener('keydown', key)
    }
  }, [ref, away])
}
