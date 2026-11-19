import type { ReactNode } from 'react'
import type { Deployment, Role, Status } from '../lib/api'
import { Link } from '../lib/router'
import { Visit } from './Icons'
import { ProjectMark } from './ProjectMark'

const roleWord: Record<Role, string> = {
  production: 'Production',
  preview: 'Preview',
  building: 'Building',
  failed: 'Failed',
  kept: 'Kept',
}

export const Badge = ({ role }: { role: Role }) => <span className={'badge ' + role}>{roleWord[role]}</span>

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
  ['Logs', '/logs'],
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
