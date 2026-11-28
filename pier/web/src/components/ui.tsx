import { useEffect, useRef, useState, type ReactNode } from 'react'
import type { Deployment, Role, Status } from '../lib/api'
import { ago, exact, plural } from '../lib/format'
import { Link } from '../lib/router'
import { ChevronLeft, ChevronRight, Search, Visit, Warning } from './Icons'
import { ProjectMark } from './ProjectMark'
import { Spinner } from './Spinner'

const roleWord: Record<Role, string> = {
  production: 'Production',
  preview: 'Preview',
  building: 'Building',
  failed: 'Failed',
  kept: 'Kept',
}

// A building deployment's badge turns while it builds.
export const Badge = ({ role }: { role: Role }) => (
  <span className={'badge ' + role}>
    {role === 'building' && <Spinner />}
    {roleWord[role]}
  </span>
)

export const statusWord: Record<Status, string> = {
  live: 'Live',
  building: 'Building',
  failed: 'Failed',
  none: 'No deployments',
}

// deploymentStatus is the one word for a deployment on its own page.
export function deploymentStatus(d: Deployment): { word: string; swatch: string } {
  if (d.status === 'building') return { word: 'Building', swatch: 'building' }
  if (d.status === 'failed') return { word: 'Failed', swatch: 'failed' }
  if (d.role === 'production') return { word: 'Live', swatch: 'live' }
  return { word: 'Ready', swatch: 'ready' }
}

const tabs = [
  ['Deployments', ''],
  ['Domains', '/domains'],
  ['Variables', '/variables'],
  ['Database', '/database'],
  ['Storage', '/storage'],
  ['Logs', '/logs'],
  ['Jobs', '/jobs'],
  ['Settings', '/settings'],
] as const

// ProjectHeader is the project's name, address and tabs.
export function ProjectHeader({ name, address, url, live, path }: {
  name: string
  address: string
  url: string
  live: boolean
  path: string
}) {
  const base = `/projects/${name}`
  const current = path.slice(base.length)
  const on = (suffix: string) => (suffix === '' ? current === '' || current.startsWith('/deployments') : current.startsWith(suffix))
  return (
    <header className="project-header">
      <div className="row">
        <ProjectMark key={name} name={name} />
        <div className="names">
          <h1 className="t-title" style={{ margin: 0 }}>
            {name}
          </h1>
          <span className="mono t-caption t-muted">{address}</span>
        </div>
        {live && (
          <a className="btn quiet visit" href={url} target="_blank" rel="noreferrer">
            Visit
            <Visit />
          </a>
        )}
      </div>
      <nav className="tabs" aria-label={`${name} sections`}>
        {tabs.map(([label, suffix]) => (
          <Link key={label} to={base + suffix} className={'tab' + (on(suffix) ? ' active' : '')}>
            {label}
          </Link>
        ))}
      </nav>
    </header>
  )
}

export function Empty({ icon, title, children }: { icon?: ReactNode; title: string; children?: ReactNode }) {
  return (
    <div className="empty">
      {icon}
      <span className="t-small t-strong">{title}</span>
      {children}
    </div>
  )
}

// Later is a section that isn't built yet: it says what will be there,
// rather than showing made-up data.
export function Later({ title, what }: { title: string; what: string }) {
  return (
    <Empty title={`${title} comes in a later version of Pier`}>
      <span className="t-caption t-muted" style={{ maxWidth: 520 }}>
        {what}
      </span>
    </Empty>
  )
}

// PageHeading is a global page's title, the sentence under it, and its
// actions at the end.
export function PageHeading({ title, note, children }: { title: string; note?: ReactNode; children?: ReactNode }) {
  return (
    <header className="page-heading">
      <div>
        <h1 className="t-title">{title}</h1>
        {note && <p className="t-caption t-muted">{note}</p>}
      </div>
      {children && <div className="page-actions">{children}</div>}
    </header>
  )
}

// When is a time in words, with the exact time on hover or focus.
export function When({ at, now, className, lower }: {
  at: string | number
  now: number
  className?: string
  lower?: boolean // inside a sentence: "yesterday", not "Yesterday"
}) {
  const iso = typeof at === 'number' ? new Date(at).toISOString() : at
  const words = ago(iso, now)
  return (
    <time className={className} dateTime={iso} data-tip={exact(iso)} data-tip-mono tabIndex={-1}>
      {lower ? words.charAt(0).toLowerCase() + words.slice(1) : words}
    </time>
  )
}

// Skeleton is a list that's loading: rows of faint bars (Components 4).
export function Skeleton({ rows = 3, tall }: { rows?: number; tall?: boolean }) {
  const widths = [38, 52, 30, 46, 34, 56]
  return (
    <div className="skeleton" aria-busy="true" aria-label="Loading">
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className={'skeleton-row' + (tall ? ' tall' : '')}>
          <i style={{ width: widths[i % widths.length] + '%' }} />
          {tall && <i style={{ width: widths[(i + 2) % widths.length] * 0.6 + '%' }} />}
        </div>
      ))}
    </div>
  )
}

// Problem is a failed load or action, said in the page above what it's about.
export function Problem({ children }: { children: ReactNode }) {
  if (!children) return null
  return (
    <div className="alert error" role="alert">
      <Warning size={20} />
      <div>
        <span className="t-caption">{children}</span>
      </div>
    </div>
  )
}

// usePaged cuts a long list into pages, and goes back to the first page
// when what's being listed changes.
export function usePaged<T>(rows: T[], per: number, resetOn: unknown) {
  const [page, setPage] = useState(0)
  useEffect(() => setPage(0), [resetOn])
  const pages = Math.max(1, Math.ceil(rows.length / per))
  const at = Math.min(page, pages - 1)
  return { at, pages, per, total: rows.length, shown: rows.slice(at * per, at * per + per), setPage }
}

// Paging sits under a list that has more than one page.
export function Paging({ paged, noun, many }: {
  paged: { at: number; pages: number; per: number; total: number; setPage: (n: number) => void }
  noun: string
  many?: string
}) {
  const { at, pages, per, total, setPage } = paged
  if (total <= per) return null
  return (
    <div className="paging">
      <span className="t-note">
        <span className="count">
          {(at * per + 1).toLocaleString('en-GB')} to {Math.min(total, (at + 1) * per).toLocaleString('en-GB')}
        </span>{' '}
        of {plural(total, noun, many)}
      </span>
      <div className="paging-buttons">
        <button className="icon-btn" disabled={at === 0} onClick={() => setPage(at - 1)} aria-label="Previous page" data-tip="Previous page">
          <ChevronLeft color={at === 0 ? '#5E667B' : '#ECEEF4'} />
        </button>
        <button className="icon-btn" disabled={at >= pages - 1} onClick={() => setPage(at + 1)} aria-label="Next page" data-tip="Next page">
          <ChevronRight size={16} color={at >= pages - 1 ? '#5E667B' : '#ECEEF4'} />
        </button>
      </div>
    </div>
  )
}

// SearchField is the Search field (Components 2), with its key hint.
export function SearchField({ value, onChange, placeholder, hotkey, width = 240 }: {
  value: string
  onChange: (v: string) => void
  placeholder: string
  hotkey?: string // a key that focuses it from anywhere on the page
  width?: number | string
}) {
  const input = useRef<HTMLInputElement>(null)
  useEffect(() => {
    if (!hotkey) return
    const key = (e: KeyboardEvent) => {
      const typing = (e.target as HTMLElement).closest('input, textarea, select, [role=dialog]')
      if (e.key === hotkey && !typing && !e.metaKey && !e.ctrlKey) {
        e.preventDefault()
        input.current?.focus()
      }
    }
    window.addEventListener('keydown', key)
    return () => window.removeEventListener('keydown', key)
  }, [hotkey])
  return (
    <label className="search-field" style={{ width }}>
      <Search size={18} />
      <input
        ref={input}
        placeholder={placeholder}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Escape') {
            onChange('')
            input.current?.blur()
          }
        }}
        aria-label={placeholder}
      />
      {hotkey && !value && <span className="key">{hotkey}</span>}
    </label>
  )
}
