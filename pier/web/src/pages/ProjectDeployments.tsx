import { useEffect, useMemo, useRef, useState } from 'react'
import { api, ApiError, type Address, type Deployment, type Project, type Session } from '../lib/api'
import { ago, plural, seconds, short, size } from '../lib/format'
import { Link, navigate } from '../lib/router'
import { Branch, ChevronLeft, ChevronRight, Info, More, Redeploy, Search, SortDown } from '../components/Icons'
import { Badge, Empty } from '../components/ui'
import { Flaps } from '../flap/Flaps'

type Filter = 'all' | 'production' | 'previews'
const PAGE = 20
const cols = '120px 190px minmax(0,1fr) 120px 90px 140px 32px'

// ProjectDeployments is the project's home (E1): what's live, what changed,
// and every deployment, newest first.
export function ProjectDeployments({ project, session, now, reload }: {
  project: Project
  session: Session
  now: number
  reload: () => void
}) {
  const [filter, setFilter] = useState<Filter>('all')
  const [busy, setBusy] = useState(false)
  const [failed, setFailed] = useState('')
  const [menu, setMenu] = useState('')

  // act moves production: promote, roll back, or undo. Nothing is rebuilt,
  // so the page reloads at once and production's tiles flip.
  const act = async (path: string, id?: string) => {
    setBusy(true)
    setFailed('')
    setMenu('')
    try {
      await api(`/api/projects/${encodeURIComponent(project.name)}/${path}`, {
        method: 'POST',
        body: JSON.stringify(id ? { id } : {}),
      })
      reload()
    } catch (e) {
      setFailed(e instanceof ApiError ? e.message : "Can't reach pierd.")
    } finally {
      setBusy(false)
    }
  }
  const prodBranch = project.production_branch || session.server.production_branch
  const [query, setQuery] = useState('')
  const [page, setPage] = useState(0)
  const search = useRef<HTMLInputElement>(null)

  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      const typing = (e.target as HTMLElement).closest('input, textarea')
      if (e.key === '/' && !typing) {
        e.preventDefault()
        search.current?.focus()
      }
    }
    window.addEventListener('keydown', key)
    return () => window.removeEventListener('keydown', key)
  }, [])

  const all = project.deployments
  const isProd = (d: Deployment) => d.target === 'production'
  const counts = { all: all.length, production: all.filter(isProd).length, previews: all.filter((d) => !isProd(d)).length }
  const rows = useMemo(() => {
    const q = query.trim().toLowerCase()
    return all.filter(
      (d) =>
        (filter === 'all' || (filter === 'production') === isProd(d)) &&
        (!q || [d.id, d.branch, d.commit, d.message].some((v) => v.toLowerCase().includes(q))),
    )
  }, [all, filter, query])
  const pages = Math.max(1, Math.ceil(rows.length / PAGE))
  const at = Math.min(page, pages - 1)
  const shown = rows.slice(at * PAGE, at * PAGE + PAGE)

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

  return (
    <main className="main">
      <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
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
            <button className="btn quiet" disabled={busy} onClick={() => act('rollback/undo')}>
              Undo rollback
            </button>
          </div>
        )}
        {failed && (
          <div className="alert error" role="alert">
            <div>
              <span className="t-caption">{failed}</span>
            </div>
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
                  disabled={busy}
                  title={`Production goes back to ${project.rollback_to}`}
                  onClick={() => act('rollback')}
                >
                  <Redeploy />
                  Roll back
                </button>
              ) : null
            }
          />
          {project.preview && (
            <AddressRow
              kind="preview"
              address={project.preview}
              now={now}
              action={
                project.preview.deployment ? (
                  <button className="btn primary" disabled={busy} onClick={() => act('promote', project.preview!.deployment!.id)}>
                    Promote to production
                  </button>
                ) : null
              }
            />
          )}
        </div>
        <Strip project={project} />
      </div>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
        <div className="toolbar">
          <div className="title">
            <span className="t-small t-strong">Deployments</span>
            <span className="mono t-caption" style={{ color: 'var(--d-note)' }}>
              {all.length}
            </span>
          </div>
          <div className="seg" role="tablist">
            {(['all', 'production', 'previews'] as Filter[]).map((f) => (
              <button
                key={f}
                className={filter === f ? 'on' : ''}
                onClick={() => {
                  setFilter(f)
                  setPage(0)
                }}
                role="tab"
                aria-selected={filter === f}
              >
                {f === 'all' ? 'All' : f === 'production' ? 'Production' : 'Previews'}
                <span className="count">{counts[f]}</span>
              </button>
            ))}
          </div>
          <label className="search-field" style={{ width: 240 }}>
            <Search size={18} />
            <input
              ref={search}
              placeholder="Search deployments"
              value={query}
              onChange={(e) => {
                setQuery(e.target.value)
                setPage(0)
              }}
              aria-label="Search deployments"
            />
            <span className="key">/</span>
          </label>
        </div>

        <div className="table" style={{ ['--cols' as string]: cols }}>
          <div className="t-head">
            <span>Deployment</span>
            <span>Branch</span>
            <span>Commit</span>
            <span>Status</span>
            <span>Build</span>
            <span style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
              Created
              <SortDown />
            </span>
            <span />
          </div>
          {shown.map((d) => (
            <div
              key={d.id}
              className="t-row clickable"
              onClick={() => navigate(`/projects/${project.name}/deployments/${d.id}`)}
            >
              <Link to={`/projects/${project.name}/deployments/${d.id}`} className="mono" onClick={(e) => e.stopPropagation()}>
                {d.id}
              </Link>
              <span className="branch mono">
                <Branch />
                <span style={{ overflow: 'hidden', textOverflow: 'ellipsis' }}>{d.branch || 'pier deploy'}</span>
              </span>
              <span title={d.message}>{d.message || <span className="mono t-muted">{short(d.commit)}</span>}</span>
              <span>
                <Badge role={d.role} />
              </span>
              <span className="mono t-muted">{d.status === 'building' ? '…' : seconds(d.build_ms)}</span>
              <span className="t-muted">{ago(d.created_at, now)}</span>
              <span className="row-more" onClick={(e) => e.stopPropagation()}>
                {d.status === 'ready' && d.role !== 'production' && (
                  <>
                    <button
                      className="icon-btn"
                      aria-label={`Actions for ${d.id}`}
                      aria-expanded={menu === d.id}
                      onClick={() => setMenu(menu === d.id ? '' : d.id)}
                    >
                      <More />
                    </button>
                    {menu === d.id && (
                      <div className="menu" style={{ right: 0, top: 38, minWidth: 200 }}>
                        <button className="menu-item" disabled={busy} onClick={() => act('promote', d.id)}>
                          Promote to production
                        </button>
                        <a className="menu-item" href={d.url} target="_blank" rel="noreferrer">
                          Visit {d.id}
                        </a>
                      </div>
                    )}
                  </>
                )}
              </span>
            </div>
          ))}
          {shown.length === 0 && (
            <div className="t-row" style={{ gridTemplateColumns: '1fr' }}>
              <span className="t-note">No deployment matches.</span>
            </div>
          )}
        </div>

        {rows.length > PAGE && (
          <div className="paging">
            <span className="t-note">
              <span className="mono" style={{ color: 'var(--d-text)' }}>
                {at * PAGE + 1} to {Math.min(rows.length, (at + 1) * PAGE)}
              </span>{' '}
              of {plural(rows.length, 'deployment')}
            </span>
            <div style={{ display: 'flex', gap: 8 }}>
              <button className="icon-btn" disabled={at === 0} onClick={() => setPage(at - 1)} aria-label="Newer">
                <ChevronLeft color={at === 0 ? '#5E667B' : '#ECEEF4'} />
              </button>
              <button className="icon-btn" disabled={at >= pages - 1} onClick={() => setPage(at + 1)} aria-label="Older">
                <ChevronRight size={16} color={at >= pages - 1 ? '#5E667B' : '#ECEEF4'} />
              </button>
            </div>
          </div>
        )}
      </div>
    </main>
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
  const note = !address
    ? 'Run pier deploy --prod to go live'
    : kind === 'production'
      ? `${rolledBack ? 'Rolled back' : 'Deployed'} ${ago(address.updated_at, now).replace(/^Y/, 'y')}`
      : `Ready ${ago(address.updated_at, now).replace(/^Y/, 'y')}`
  return (
    <div className="address-row">
      <div className="kind">
        <div>
          <i className={'swatch ' + (address ? kind : '')} />
          <span className="t-small t-strong">{label}</span>
        </div>
        <span className="t-note">{note}</span>
      </div>
      <div className="where">
        {address ? (
          <a className="mono t-caption" href={address.url} target="_blank" rel="noreferrer">
            {address.host}
          </a>
        ) : (
          <span className="mono t-caption" style={{ color: 'var(--d-faint)' }}>
            {fallbackHost}
          </span>
        )}
        {d && (
          <span className="t-note serving">
            {kind === 'production' ? (
              // Production's deployment is set in tiles: a deploy, promote or
              // rollback flips only this, while the address stays put.
              <button className="serving-id" onClick={() => navigate(`/projects/${d.project}/deployments/${d.id}`)}>
                <Flaps size="sm" columns={[{ width: 6 }]} values={[d.id]} label={`Deployment ${d.id}`} />
              </button>
            ) : (
              <span
                className="mono"
                style={{ cursor: 'pointer' }}
                onClick={() => navigate(`/projects/${d.project}/deployments/${d.id}`)}
              >
                {d.id}
              </span>
            )}
            <span>
              {d.source === 'cli' ? (d.branch ? `uploaded from ${d.branch}` : 'uploaded with pier deploy') : `from ${d.branch}`}
              {d.message ? `: ${d.message}` : ''}
            </span>
          </span>
        )}
      </div>
      {action && <div style={{ flex: 'none', display: 'flex' }}>{action}</div>}
    </div>
  )
}

function Strip({ project }: { project: Project }) {
  const { stats } = project
  const builds = stats.builds
  if (builds.length === 0) return null
  const longest = Math.max(...builds.map((b) => b.ms), 1)
  return (
    <div className="panel strip" style={{ ['--cells' as string]: 2 }}>
      <div>
        <span className="t-note">Build time</span>
        <div className="figure">
          <span className="value">{seconds(stats.median_ms)}</span>
          <div className="bars" aria-hidden>
            {builds.map((b, i) => (
              <i
                key={b.id}
                title={`${b.id}: ${seconds(b.ms)}${b.status === 'failed' ? ', failed' : ''}`}
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
        <span className="value">{stats.kept}</span>
        <span className="t-note">{size(stats.bytes)} on disk</span>
      </div>
    </div>
  )
}
