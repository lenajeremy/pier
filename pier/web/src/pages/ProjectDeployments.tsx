import { useEffect, useMemo, useState } from 'react'
import { api, type Address, type Deployment, type Project, type Session } from '../lib/api'
import { promote, redeploy, restart, rollBack, undoRollBack } from '../lib/actions'
import { localDay } from '../lib/calendar'
import { plural, seconds, short, size } from '../lib/format'
import { Link, navigate } from '../lib/router'
import { ArrowUp, Branch, Copy, Info, Redeploy, RollBack, Rows, SortDown, Terminal, Visit, Warning } from '../components/Icons'
import { Menu, type MenuItem } from '../components/Menu'
import { Spinner, useBusy } from '../components/Spinner'
import { copy } from '../components/Toast'
import { Badge, Empty, Paging, SearchField, usePaged, When } from '../components/ui'
import { Flaps } from '../flap/Flaps'

type Filter = 'all' | 'production' | 'previews'
const PAGE = 20

// ProjectDeployments is the project's home (E1): what's live, what changed,
// and every deployment, newest first.
export function ProjectDeployments({ project, session, now, reload }: {
  project: Project
  session: Session
  now: number
  reload: () => void
}) {
  const [filter, setFilter] = useState<Filter>('all')
  const [query, setQuery] = useState('')
  const [oldest, setOldest] = useState(false)
  const [busy, run] = useBusy<'undo' | 'restart'>()

  const prodBranch = project.production_branch || session.server.production_branch
  const live = project.production?.deployment
  const target = { project: project.name, host: project.address, current: live?.id, branch: prodBranch }
  // A preview that's since been promoted is production's own deployment:
  // there's nothing left to promote.
  const preview = project.preview?.deployment && project.preview.deployment.id !== live?.id ? project.preview : null

  const all = project.deployments
  const isProd = (d: Deployment) => d.target === 'production'
  const counts = { all: all.length, production: all.filter(isProd).length, previews: all.filter((d) => !isProd(d)).length }
  const rows = useMemo(() => {
    const q = query.trim().toLowerCase()
    const found = all.filter(
      (d) =>
        (filter === 'all' || (filter === 'production') === isProd(d)) &&
        (!q || [d.id, d.branch, d.commit, d.message].some((v) => v.toLowerCase().includes(q))),
    )
    return oldest ? [...found].reverse() : found
  }, [all, filter, query, oldest])
  const paged = usePaged(rows, PAGE, filter + '\u0000' + query + oldest)

  if (all.length === 0) {
    return (
      <main className="main">
        <Empty title="Nothing deployed yet">
          <span className="t-caption t-muted" style={{ maxWidth: 480 }}>
            In the folder you want to deploy, run <span className="mono nowrap">pier deploy</span> for a preview, or add{' '}
            <span className="mono nowrap">--prod</span> to go straight to {project.address}.
          </span>
          <span className="command" style={{ textAlign: 'left' }}>
            pier login {session.server.address}
            <br />
            pier deploy --project {project.name} --prod
          </span>
        </Empty>
      </main>
    )
  }

  const container = project.container && live?.runtime && project.container.state !== 'running' ? project.container : null

  return (
    <main className="main">
      <div className="stack">
        {project.rolled_back_from && (
          <div className="alert info">
            <Info />
            <div>
              <span className="t-small t-strong">New pushes to {prodBranch} won't go live on their own</span>
              <span className="t-caption t-muted">
                While production is rolled back, production builds get their own address but don't replace it. Promote one, or
                undo to go back to <span className="mono">{project.rolled_back_from}</span>.
              </span>
            </div>
            <button className="btn quiet" disabled={busy === 'undo'} onClick={() => run('undo', () => undoRollBack(project.name, reload))}>
              {busy === 'undo' && <Spinner />}
              {busy === 'undo' ? 'Undoing…' : 'Undo rollback'}
            </button>
          </div>
        )}
        {container && live && (
          <div className="alert error" role="alert">
            {container.state === 'restarting' ? (
              <span className="alert-spinner">
                <Spinner large />
              </span>
            ) : (
              <Warning size={20} />
            )}
            <div>
              <span className="t-small t-strong">
                {container.state === 'oom_killed' || container.oom_killed
                  ? "Production's container ran out of memory"
                  : container.state === 'restarting'
                    ? "Production's container is restarting"
                    : "Production's container isn't running"}
              </span>
              <span className="t-caption t-muted">
                {container.oom_killed
                  ? 'Raise the memory limit in Settings, or read the runtime logs to see what used it.'
                  : `It's ${container.state.replace('_', ' ')}${container.restarts ? `, after ${plural(container.restarts, 'restart')}` : ''}.`}
              </span>
            </div>
            <button
              className="btn quiet"
              disabled={busy === 'restart'}
              onClick={() => run('restart', () => restart(project.name, live.id, reload))}
            >
              {busy === 'restart' && <Spinner />}
              {busy === 'restart' ? 'Restarting…' : 'Restart'}
            </button>
          </div>
        )}
        <div className="panel">
          <AddressRow
            kind="production"
            address={project.production}
            fallbackHost={project.address}
            now={now}
            rolledBack={!!project.rolled_back_from}
            action={
              project.rollback_to ? (
                <button
                  className="btn quiet icon"
                  data-tip={`Back to ${project.rollback_to}`}
                  onClick={() => rollBack(target, project.rollback_to, reload)}
                >
                  <RollBack />
                  Roll back
                </button>
              ) : null
            }
          />
          {preview && (
            <AddressRow
              kind="preview"
              address={preview}
              now={now}
              action={
                <button className="btn primary" onClick={() => promote(target, preview.deployment!, reload)}>
                  Promote to production
                </button>
              }
            />
          )}
        </div>
        <Strip project={project} today={localDay(new Date(now)).getTime()} />
      </div>

      <div className="stack">
        <div className="toolbar">
          <div className="title">
            <h2 className="t-small t-strong">Deployments</h2>
            <span className="count">{all.length.toLocaleString('en-GB')}</span>
          </div>
          <div className="seg" role="tablist" aria-label="Which deployments">
            {(['all', 'production', 'previews'] as Filter[]).map((f) => (
              <button key={f} className={filter === f ? 'on' : ''} onClick={() => setFilter(f)} role="tab" aria-selected={filter === f}>
                {f === 'all' ? 'All' : f === 'production' ? 'Production' : 'Previews'}
                <span className="count">{counts[f].toLocaleString('en-GB')}</span>
              </button>
            ))}
          </div>
          <SearchField value={query} onChange={setQuery} placeholder="Search deployments" hotkey="/" />
        </div>

        <div className="table deployments">
          <div className="t-head">
            <span>Deployment</span>
            <span>Branch</span>
            <span>Commit</span>
            <span>Status</span>
            <span>Build</span>
            <button
              className="sort"
              onClick={() => setOldest(!oldest)}
              aria-label={oldest ? 'Created, oldest first. Show the newest first' : 'Created, newest first. Show the oldest first'}
              data-tip={oldest ? 'Oldest first' : 'Newest first'}
            >
              Created
              <span className={'sort-arrow' + (oldest ? ' up' : '')}>
                <SortDown />
              </span>
            </button>
            <span />
          </div>
          {paged.shown.map((d) => (
            <DeploymentRow key={d.id} d={d} now={now} target={target} reload={reload} />
          ))}
          {rows.length === 0 && (
            <div className="t-empty">
              <span className="t-caption t-muted">
                {query ? `No deployment matches “${query.trim()}”.` : `No ${filter === 'production' ? 'production deployments' : 'previews'} yet.`}
              </span>
              {(query || filter !== 'all') && (
                <button
                  className="btn quiet small"
                  onClick={() => {
                    setQuery('')
                    setFilter('all')
                  }}
                >
                  Show all
                </button>
              )}
            </div>
          )}
        </div>
        <Paging paged={paged} noun="deployment" />
      </div>
    </main>
  )
}

type Target = { project: string; host: string; current?: string; branch: string }

function DeploymentRow({ d, now, target, reload }: { d: Deployment; now: number; target: Target; reload: () => void }) {
  const page = `/projects/${d.project}/deployments/${d.id}`
  const ready = d.status === 'ready'
  const items: MenuItem[] = [
    ...(ready && d.role !== 'production'
      ? [{ label: 'Promote to production', icon: <ArrowUp />, onSelect: () => promote(target, d, reload) } as MenuItem]
      : []),
    ...(ready
      ? [
          { label: 'Visit', icon: <Visit color="#A3ABBE" />, href: d.url } as MenuItem,
          { label: 'Copy address', icon: <Copy />, onSelect: () => copy(d.url) } as MenuItem,
        ]
      : []),
    { label: 'View build log', icon: <Rows />, to: page },
    ...(d.status !== 'building'
      ? [{ label: 'Redeploy', icon: <Redeploy color="#A3ABBE" />, onSelect: () => redeploy(target, d) } as MenuItem]
      : []),
  ]
  const cli = d.source === 'cli'
  return (
    <div className="t-row clickable" onClick={() => navigate(page)}>
      <Link to={page} className="mono" onClick={(e) => e.stopPropagation()}>
        {d.id}
      </Link>
      <span className="branch" data-tip={cli ? 'Uploaded with pier deploy' : undefined}>
        {cli ? <Terminal /> : <Branch />}
        <span className="mono">{d.branch || 'pier deploy'}</span>
      </span>
      <span className="commit">
        {d.message ? (
          <span>{d.message}</span>
        ) : d.commit ? (
          <span className="mono t-muted">{short(d.commit)}</span>
        ) : (
          <span className="t-quiet">No commit message</span>
        )}
      </span>
      <span>
        <Badge role={d.role} />
      </span>
      <span className="mono t-muted">{d.status === 'building' ? '…' : seconds(d.build_ms)}</span>
      <When at={d.created_at} now={now} className="t-muted" />
      <span className="row-more" onClick={(e) => e.stopPropagation()}>
        <Menu label={`Actions for ${d.id}`} items={items} className="icon-btn bare" />
      </span>
    </div>
  )
}

function AddressRow({ kind, address, fallbackHost, now, rolledBack, action }: {
  kind: 'production' | 'preview'
  address: Address | null
  fallbackHost?: string
  now: number
  rolledBack?: boolean
  action?: React.ReactNode
}) {
  const d = address?.deployment
  const label = kind === 'production' ? 'Production' : 'Latest preview'
  const open = () => d && navigate(`/projects/${d.project}/deployments/${d.id}`)
  return (
    <div className="address-row">
      <div className="kind">
        <div>
          <i className={'swatch ' + (address ? kind : '')} />
          <span className="t-small t-strong">{label}</span>
        </div>
        <span className="t-note">
          {!address ? (
            'Run pier deploy --prod to go live'
          ) : (
            <>
              {kind === 'production' ? (rolledBack ? 'Rolled back' : 'Deployed') : 'Ready'} <When at={address.updated_at} now={now} lower />
            </>
          )}
        </span>
      </div>
      <div className="where">
        {address ? (
          <span className="host">
            <a className="mono t-caption" href={address.url} target="_blank" rel="noreferrer">
              {address.host}
            </a>
            <button className="copy" onClick={() => copy(address.url)} aria-label={`Copy ${address.host}`} data-tip="Copy address">
              <Copy size={14} />
            </button>
          </span>
        ) : (
          <span className="mono t-caption t-quiet">{fallbackHost}</span>
        )}
        {d && (
          <span className="t-note serving">
            {kind === 'production' ? (
              // Production's deployment is set in tiles: a deploy, promote or
              // rollback flips only this, while the address stays put.
              <button className="serving-id" onClick={open} aria-label={`Open deployment ${d.id}`}>
                <Flaps size="sm" columns={[{ width: 6 }]} values={[d.id]} label={`Deployment ${d.id}`} />
              </button>
            ) : (
              <button className="mono serving-link" onClick={open}>
                {d.id}
              </button>
            )}
            <span>
              {d.source === 'cli' ? (d.branch ? `uploaded from ${d.branch}` : 'uploaded with pier deploy') : `from ${d.branch}`}
              {d.message ? `: ${d.message}` : ''}
            </span>
          </span>
        )}
      </div>
      {action && <div className="address-action">{action}</div>}
    </div>
  )
}

type Day = { requests: number; e4xx: number; e5xx: number }

// useTraffic counts yesterday's requests and the day before's, in the
// viewer's own calendar days.
function useTraffic(project: string, today: number): { yesterday: Day; before: Day } | null {
  const [t, setT] = useState<{ yesterday: Day; before: Day } | null>(null)
  useEffect(() => {
    let active = true
    setT(null)
    const day = (n: number) => localDay(new Date(today), -n).getTime()
    const get = (from: number, to: number) =>
      api<Day>(`/api/projects/${encodeURIComponent(project)}/requests/chart?from=${from}&to=${to}`)
    Promise.all([get(day(1), day(0)), get(day(2), day(1))])
      .then(([yesterday, before]) => {
        if (active) setT({ yesterday, before })
      })
      .catch(() => {
        if (active) setT(null)
      })
    return () => {
      active = false
    }
  }, [project, today])
  return t
}

function compared(now: number, before: number): string {
  if (before === 0) return now === 0 ? 'None the day before either' : 'None the day before'
  const change = Math.round(((now - before) / before) * 100)
  if (Math.abs(change) < 3) return 'About the same as the day before'
  return `${Math.abs(change)}% ${change > 0 ? 'more' : 'fewer'} than the day before`
}

function Strip({ project, today }: { project: Project; today: number }) {
  const { stats } = project
  const builds = stats.builds
  const traffic = useTraffic(project.name, today)
  if (builds.length === 0) return null
  const longest = Math.max(...builds.map((b) => b.ms), 1)
  // Traffic shows once the site has had any in the last two days.
  const t = traffic && traffic.yesterday.requests + traffic.before.requests > 0 ? traffic : null
  const y = t?.yesterday
  const errors = y ? y.e4xx + y.e5xx : 0
  const logs = `/projects/${project.name}/logs?day=yesterday`
  return (
    <div className="panel strip" style={{ ['--cells' as string]: t ? 4 : 2 }}>
      {t && y && (
        <>
          <Link to={logs} className="strip-link">
            <span className="t-note">Requests yesterday</span>
            <span className="value">{y.requests.toLocaleString('en-GB')}</span>
            <span className="t-note">{compared(y.requests, t.before.requests)}</span>
          </Link>
          <Link to={logs + '&status=errors'} className="strip-link">
            <span className="t-note">Errors yesterday</span>
            <span className="value">{errors.toLocaleString('en-GB')}</span>
            <span className="t-note">
              {errors === 0
                ? 'No 4xx or 5xx responses.'
                : y.e5xx === 0
                  ? 'All 4xx. None from the server.'
                  : `${y.e5xx.toLocaleString('en-GB')} from the server (5xx).`}
            </span>
          </Link>
        </>
      )}
      <div>
        <span className="t-note">Build time</span>
        <div className="figure">
          <span className="value">{seconds(stats.median_ms)}</span>
          <div className="bars" aria-hidden>
            {builds.map((b, i) => (
              <i
                key={b.id}
                data-tip={`${b.id}: ${seconds(b.ms)}${b.status === 'failed' ? ', failed' : ''}`}
                className={b.status === 'failed' ? 'failed' : i === builds.length - 1 ? 'newest' : ''}
                style={{ height: Math.max(4, Math.round((b.ms / longest) * 23)) }}
              />
            ))}
          </div>
        </div>
        <span className="t-note">
          {builds.length === 1 ? 'The only build so far.' : `Median of the last ${builds.length}.`}
          {stats.failed > 0 && ` ${stats.failed === 1 ? 'One' : stats.failed} failed.`}
        </span>
      </div>
      <div>
        <span className="t-note">Kept deployments</span>
        <span className="value">{stats.kept.toLocaleString('en-GB')}</span>
        <span className="t-note">{size(stats.bytes)} on disk</span>
      </div>
    </div>
  )
}
