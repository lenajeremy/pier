import { useEffect, useRef, useState, type ReactNode } from 'react'
import type { ProjectSummary, Session } from '../lib/api'
import { signOut } from '../lib/api'
import { Link, navigate } from '../lib/router'
import * as I from './Icons'
import { useData } from '../lib/data'
import type { Sample } from './ServerCharts'
import { ProjectMark } from './ProjectMark'

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
  const recent = useData<{samples: Sample[]}>('/api/server/history?range=latest', () => true, 60000)
  const sample = recent.data?.samples.at(-1)
  const pressure = sample && (sample.disk >= 85 ? 'Disk usage above 85%' : sample.memory >= 90 ? 'Memory usage above 90%' : '')
  const [menuOpen, setMenuOpen] = useState(false)
  const [query, setQuery] = useState('')
  useEffect(() => setMenuOpen(false), [path])
  const search = useRef<HTMLInputElement>(null)

  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        search.current?.focus()
      }
    }
    window.addEventListener('keydown', key)
    return () => window.removeEventListener('keydown', key)
  }, [])

  const shown = projects.filter((p) => p.name.includes(query.trim().toLowerCase()))
  const active = (to: string) => !project && (to === '/' ? path === '/' || path === '/new' : path.startsWith(to))

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
        <label className="side-search">
          <I.Search />
          <input
            ref={search}
            placeholder="Search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && shown[0]) {
                navigate(`/projects/${shown[0].name}`)
                setQuery('')
                search.current?.blur()
              }
              if (e.key === 'Escape') {
                setQuery('')
                search.current?.blur()
              }
            }}
            aria-label="Search projects"
          />
          <span className="kbd">⌘K</span>
        </label>
        <nav className="nav" aria-label="Server">
          {globals.map(({ to, label, icon: Icon }) => (
            <Link key={to} to={to} className={'nav-item' + (active(to) ? ' active' : '')}>
              <Icon />
              <span className="grow">{label}</span>
              {to === '/server' && pressure && <span className="server-warning" title={pressure} aria-label={pressure}>●</span>}
            </Link>
          ))}
        </nav>
        <nav className="nav" aria-label="Projects">
          <div className="side-projects-head">
            <span className="t-note">Projects</span>
            <Link to="/new" className="icon-btn-24" aria-label="New project" title="New project">
              <I.Plus />
            </Link>
          </div>
          {shown.map((p) => (
            <Link
              key={p.name}
              to={`/projects/${p.name}`}
              className={'side-project' + (p.name === project ? ' active' : '')}
            >
              <i className={'swatch ' + p.status} />
              <span>{p.name}</span>
            </Link>
          ))}
          {projects.length > 0 && shown.length === 0 && (
            <span className="side-project t-note">No project matches</span>
          )}
        </nav>
        <div className="side-spacer" />
        <Account session={session} />
      </aside>
      <div className="content">{children}</div>
    </div>
  )
}

function Switcher({ session, projects, project }: { session: Session; projects: ProjectSummary[]; project?: string }) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  useClickAway(ref, () => setOpen(false))
  return (
    <div ref={ref} style={{ position: 'relative' }}>
      <button className="switcher" onClick={() => setOpen(!open)} aria-expanded={open}>
        {project ? (
          <ProjectMark name={project} />
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
        <div className="menu" style={{ top: 52, left: 0, right: 0 }}>
          <Link to="/" className="menu-item" onClick={() => setOpen(false)}>
            <span className="switcher-mark" style={{ width: 24, height: 24, borderRadius: 6 }}>
              <I.Mark size={14} />
            </span>
            All projects
          </Link>
          {projects.length > 0 && <div className="menu-sep" />}
          {projects.map((p) => (
            <Link key={p.name} to={`/projects/${p.name}`} className="menu-item" onClick={() => setOpen(false)}>
              <i className={'swatch ' + p.status} />
              {p.name}
            </Link>
          ))}
        </div>
      )}
    </div>
  )
}

function Account({ session }: { session: Session }) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  useClickAway(ref, () => setOpen(false))
  const out = async () => {
    try {
      await signOut()
    } finally {
      window.dispatchEvent(new Event('pier:signed-out'))
    }
  }
  return (
    <div ref={ref} style={{ position: 'relative' }}>
      <button className="account" onClick={() => setOpen(!open)} aria-expanded={open}>
        <div>
          <span className="t-small">{session.user?.name || 'Owner'}</span>
          <span className="t-note">{session.user?.email}</span>
        </div>
        <I.UpDown size={16} />
      </button>
      {open && (
        <div className="menu" style={{ bottom: 52, left: 0, right: 0 }}>
          <button className="menu-item" onClick={out}>
            Sign out
          </button>
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
