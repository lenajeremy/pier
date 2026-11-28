import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { api, signOut, type Deployment, type Project, type ProjectSummary } from '../lib/api'
import { rollBack } from '../lib/actions'
import { navigate } from '../lib/router'
import { useRole } from '../lib/role'
import * as I from './Icons'

type Item = {
  group: string
  label: string
  icon: ReactNode
  hint?: string // a CLI command, or where it is
  mono?: boolean // the hint is a command
  run: () => void
}

const pages = [
  { to: '/', label: 'Projects', icon: I.Projects },
  { to: '/activity', label: 'Activity', icon: I.Activity },
  { to: '/integrations', label: 'Integrations', icon: I.Integrations },
  { to: '/shared-variables', label: 'Shared variables', icon: I.Braces },
  { to: '/members', label: 'Members', icon: I.Members },
  { to: '/server', label: 'Server', icon: I.Server },
  { to: '/alerts', label: 'Alerts', icon: I.Bell },
  { to: '/settings', label: 'Settings', icon: I.Settings },
]
const tabs = [
  ['Deployments', ''],
  ['Domains', '/domains'],
  ['Variables', '/variables'],
  ['Database', '/database'],
  ['Storage', '/storage'],
  ['Analytics', '/analytics'],
  ['Jobs', '/jobs'],
  ['Settings', '/settings'],
]

// CommandMenu is the Command menu (Components 4): ⌘K anywhere in the
// dashboard. It finds projects, pages and the open project's deployments,
// and runs a few actions, each with the CLI command that does the same.
export function CommandMenu({ projects, project, branch, onClose }: {
  projects: ProjectSummary[]
  project?: string // the open project
  branch: string // the server's production branch
  onClose: () => void
}) {
  const { change } = useRole()
  const [query, setQuery] = useState('')
  const [at, setAt] = useState(0)
  const [open, setOpen] = useState<Project | null>(null)
  const list = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!project) return
    let active = true
    api<Project>(`/api/projects/${encodeURIComponent(project)}`)
      .then((p) => active && setOpen(p))
      .catch(() => {})
    return () => {
      active = false
    }
  }, [project])

  useEffect(() => {
    const overflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => {
      document.body.style.overflow = overflow
    }
  }, [])

  const items = useMemo(() => {
    const q = query.trim().toLowerCase()
    const has = (...text: (string | undefined)[]) => !q || text.some((t) => t?.toLowerCase().includes(q))
    const go = (to: string) => () => navigate(to)
    const out: Item[] = []

    // Actions come first, as in the design: they're what a typed verb means.
    if (change && open?.rollback_to && has('roll back', 'rollback', open.name)) {
      out.push({
        group: 'Actions',
        label: `Roll back ${open.name} to ${open.rollback_to}`,
        icon: <I.RollBack size={18} color="#F49CCB" />,
        hint: `pier rollback ${open.name}`,
        mono: true,
        run: () =>
          rollBack(
            { project: open.name, host: open.address, current: open.production?.deployment?.id, branch: open.production_branch || branch },
            open.rollback_to,
            () => navigate(`/projects/${open.name}`),
          ),
      })
    }
    if (change && has('new project', 'create', 'import')) {
      out.push({ group: 'Actions', label: 'New project', icon: <I.Plus size={18} color="#A3ABBE" />, run: go('/new') })
    }
    if (q && has('sign out', 'log out')) {
      out.push({
        group: 'Actions',
        label: 'Sign out',
        icon: <I.Close size={18} />,
        hint: 'pier logout',
        mono: true,
        run: () => signOut().finally(() => window.dispatchEvent(new Event('pier:signed-out'))),
      })
    }

    if (open) {
      for (const [label, suffix] of tabs) {
        if (has(label, open.name)) {
          out.push({
            group: open.name,
            label,
            icon: <I.ChevronRight size={18} color="#A3ABBE" />,
            run: go(`/projects/${open.name}${suffix}`),
          })
        }
      }
    }
    for (const p of projects) {
      if (p.name !== project && has(p.name, p.address)) {
        out.push({
          group: 'Projects',
          label: p.name,
          icon: <i className={'swatch ' + p.status} />,
          hint: p.address,
          run: go(`/projects/${p.name}`),
        })
      }
    }
    for (const p of pages) {
      if (has(p.label)) out.push({ group: 'Pages', label: p.label, icon: <p.icon size={18} color="#A3ABBE" />, run: go(p.to) })
    }
    if (open && q) {
      const found = open.deployments.filter((d: Deployment) => has(d.id, d.branch, d.message, d.commit)).slice(0, 6)
      for (const d of found) {
        out.push({
          group: 'Deployments',
          label: `${d.id}${d.message ? ', ' + d.message : ''}`,
          icon: <I.Layers size={18} />,
          hint: d.branch || 'pier deploy',
          run: go(`/projects/${open.name}/deployments/${d.id}`),
        })
      }
    }
    return out.slice(0, 40)
  }, [query, projects, project, open, branch, change])

  useEffect(() => setAt(0), [query])
  useEffect(() => {
    list.current?.querySelector('.command-item.on')?.scrollIntoView({ block: 'nearest' })
  }, [at])

  const run = (item: Item | undefined) => {
    if (!item) return
    onClose()
    item.run()
  }
  const key = (e: React.KeyboardEvent) => {
    if (e.key === 'Escape') {
      e.preventDefault()
      onClose()
    } else if (e.key === 'ArrowDown') {
      e.preventDefault()
      setAt((at + 1) % Math.max(1, items.length))
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setAt((at - 1 + items.length) % Math.max(1, items.length))
    } else if (e.key === 'Enter') {
      e.preventDefault()
      run(items[at])
    }
  }

  let group = ''
  return createPortal(
    <div className="scrim top" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="command-menu" role="dialog" aria-modal="true" aria-label="Find anything, or run a command" onKeyDown={key}>
        <label className="command-input">
          <I.Search size={18} />
          <input
            autoFocus
            placeholder="Find a project, a page or a deployment"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            spellCheck={false}
            autoComplete="off"
            role="combobox"
            aria-expanded="true"
            aria-controls="command-list"
            aria-activedescendant={items[at] ? 'command-' + at : undefined}
          />
          <span className="command-key">esc</span>
        </label>
        <div className="command-list" id="command-list" role="listbox" ref={list}>
          {items.length === 0 && <div className="command-none t-caption t-muted">Nothing matches “{query.trim()}”.</div>}
          {items.map((item, i) => {
            const head = item.group !== group ? item.group : ''
            group = item.group
            return (
              <div key={item.group + item.label + i}>
                {head && <div className="menu-label">{head}</div>}
                <button
                  id={'command-' + i}
                  type="button"
                  role="option"
                  aria-selected={i === at}
                  tabIndex={-1}
                  className={'command-item' + (i === at ? ' on' : '')}
                  onMouseMove={() => at !== i && setAt(i)}
                  onClick={() => run(item)}
                >
                  {item.icon}
                  <span className="grow">
                    <Matched text={item.label} query={query.trim()} />
                  </span>
                  {item.hint && <span className={'command-hint' + (item.mono ? ' mono' : '')}>{item.hint}</span>}
                </button>
              </div>
            )
          })}
        </div>
        <div className="command-foot">
          <span>
            <span className="mono">↑↓</span> move
          </span>
          <span>
            <span className="mono">↵</span> open
          </span>
          <span>An action shows the pier command that does the same.</span>
        </div>
      </div>
    </div>,
    document.body,
  )
}

// Matched makes the letters that were typed bright.
function Matched({ text, query }: { text: string; query: string }) {
  const i = query ? text.toLowerCase().indexOf(query.toLowerCase()) : -1
  if (i < 0) return <>{text}</>
  return (
    <>
      {text.slice(0, i)}
      <mark>{text.slice(i, i + query.length)}</mark>
      {text.slice(i + query.length)}
    </>
  )
}
