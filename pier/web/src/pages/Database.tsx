import { useState, type FormEvent } from 'react'
import { api, type DatabaseBackup, type DatabaseStatus, type DatabaseView, type PreviewDatabase } from '../lib/api'
import { failed } from '../lib/actions'
import { useData } from '../lib/data'
import { size } from '../lib/format'
import { navigate } from '../lib/router'
import { Copy, Info, RollBack, Trash } from '../components/Icons'
import { confirm, Dialog } from '../components/Dialog'
import { Menu, type MenuItem } from '../components/Menu'
import { Spinner, useBusy } from '../components/Spinner'
import { copy, toast } from '../components/Toast'
import { Empty, Paging, Problem, Skeleton, usePaged, When } from '../components/ui'
import { Gate, useRole, viewerCant } from '../lib/role'
import { DataView } from './DatabaseData'
import { QueryView } from './DatabaseQuery'

const PAGE = 10

export const databasePath = (project: string) => `/api/projects/${encodeURIComponent(project)}/database`

type View = 'overview' | 'data' | 'query'

// state is how the server is doing, as a swatch and a word.
function state(c: DatabaseStatus['container']): { swatch: string; word: string } {
  if (c.state === 'running' && c.health === 'starting') return { swatch: 'building', word: 'Starting' }
  if (c.state === 'running' && c.health === 'unhealthy') return { swatch: 'failed', word: 'Not answering' }
  if (c.state === 'running') return { swatch: 'done', word: 'Running' }
  if (c.state === 'restarting') return { swatch: 'building', word: 'Restarting' }
  if (c.state === 'missing') return { swatch: 'failed', word: 'Missing' }
  return { swatch: 'failed', word: 'Stopped' }
}

const reasons: Record<DatabaseBackup['reason'], string> = {
  nightly: 'Nightly',
  manual: 'By hand',
  'before-restore': 'Before a restore',
  'before-delete': 'Before deleting',
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="detail">
      <span className="t-note">{label}</span>
      <div className="t-caption" style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
        {children}
      </div>
    </div>
  )
}

const memory = (n: number) => (n >= 1024 ? `${n / 1024} GB` : `${n} MB`)

// Database is the project's Database tab: its Postgres server, its data,
// and a place to run SQL.
export function Database({ project, now }: { project: string; now: number }) {
  const { data, error, reload } = useData<DatabaseView>(databasePath(project), () => true, (d) =>
    d.database && (d.database.container.health === 'starting' || d.backups.some((b) => b.state === 'running')) ? 2000 : 10_000,
  )
  const [adding, setAdding] = useState(false)
  const [view, setView] = useState<View>('overview')
  const [db, setDb] = useState('app')
  const [draft, setDraft] = useState<string>()
  const { change } = useRole()

  if (error) {
    return (
      <main className="main">
        <Problem>{error.message}</Problem>
      </main>
    )
  }
  if (!data) {
    return (
      <main className="main">
        <div className="panel">
          <Skeleton rows={4} tall />
        </div>
      </main>
    )
  }
  const d = data.database
  if (!d) {
    return (
      <main className="main">
        <Empty title="No database yet">
          <span className="t-caption t-muted">Postgres for {project} alone. It idles at about 15 MB of memory.</span>
          <Gate allow={change} reason={viewerCant('add databases')}>
            <button className="btn primary" onClick={() => setAdding(true)}>
              Add Postgres
            </button>
          </Gate>
        </Empty>
        {data.backups.length > 0 && <Backups project={project} data={data} now={now} reload={reload} restorable={false} />}
        {adding && (
          <MemoryDialog
            title="Add a Postgres database"
            action="Add Postgres"
            working="Starting Postgres…"
            choices={data.memory_choices}
            initial={data.default_memory_mb}
            onClose={() => setAdding(false)}
            run={async (memory_mb) => {
              await api(databasePath(project), { method: 'POST', body: JSON.stringify({ memory_mb }) })
              setAdding(false)
              reload()
              toast(`Added Postgres to ${project}`)
            }}
          />
        )}
      </main>
    )
  }

  const databases = [{ name: 'app', label: 'Production', swatch: 'live' }, ...d.previews.map((p) => ({ name: p.name, label: p.branch || 'pier deploy previews', swatch: 'building' }))]
  const chosen = databases.find((x) => x.name === db) ?? databases[0]
  const views: [View, string][] = [
    ['overview', 'Overview'],
    ['data', 'Data'],
    ['query', 'Query'],
  ]
  return (
    <main className="main" style={{ gap: 20 }}>
      <div className="db-views">
        <div className="seg" role="tablist" aria-label="Database">
          {views.map(([v, label]) =>
            v === 'overview' || change ? (
              <button key={v} role="tab" aria-selected={view === v} className={view === v ? 'on' : ''} onClick={() => setView(v)}>
                {label}
              </button>
            ) : (
              <Gate key={v} allow={false} reason={viewerCant('see the data in databases')}>
                <button role="tab" aria-selected={false}>
                  {label}
                </button>
              </Gate>
            ),
          )}
        </div>
        {view !== 'overview' && (
          <Menu
            label="Choose a database"
            align="end"
            className="db-picker"
            items={databases.map((x) => ({ label: `${x.label}  ${x.name}`, onSelect: () => setDb(x.name) }))}
          >
            <i className={'swatch ' + chosen.swatch} aria-hidden />
            <span>{chosen.label}</span>
            <span className="mono t-note">{chosen.name}</span>
          </Menu>
        )}
      </div>
      {view === 'overview' ? (
        <Overview project={project} data={data} d={d} now={now} reload={reload} />
      ) : view === 'data' ? (
        <DataView project={project} db={chosen.name} onQuery={(sql) => (setDraft(sql), setView('query'))} />
      ) : (
        <QueryView project={project} db={chosen.name} role={chosen.name} draft={draft} key={chosen.name} />
      )}
    </main>
  )
}

function Overview({ project, data, d, now, reload }: { project: string; data: DatabaseView; d: DatabaseStatus; now: number; reload: () => void }) {
  const [resizing, setResizing] = useState(false)
  const [busy, run] = useBusy<'backup'>()
  const { change } = useRole()
  const look = state(d.container)
  const running = data.backups.some((b) => b.state === 'running')
  const redeploy = () =>
    confirm({
      title: 'Redeploy production?',
      body: <p>The new build gets DATABASE_URL. It goes live once it works.</p>,
      action: 'Redeploy',
      working: 'Starting the build…',
      run: async () => {
        const r = await api<{ id?: string }>(`/api/projects/${encodeURIComponent(project)}/redeploy`, { method: 'POST', body: '{}' })
        if (r.id) navigate(`/projects/${project}/deployments/${r.id}`)
      },
    })
  const restart = () =>
    confirm({
      title: 'Restart the database?',
      body: <p>It takes a few seconds. Apps reconnect, and the data stays.</p>,
      action: 'Restart',
      working: 'Restarting…',
      run: async () => {
        await api(`${databasePath(project)}/restart`, { method: 'POST' })
        reload()
      },
    }).then((ok) => ok && toast('Restarted the database'))
  const remove = () =>
    confirm({
      title: `Delete ${project}'s database?`,
      body: <p>Its data and every preview database are deleted. A last backup is kept.</p>,
      action: 'Delete database',
      working: 'Backing up and deleting…',
      danger: true,
      run: async () => {
        await api(databasePath(project), { method: 'DELETE' })
        reload()
      },
    }).then((ok) => ok && toast('Deleted the database'))
  const backUp = () =>
    run('backup', async () => {
      try {
        const b = await api<DatabaseBackup>(`${databasePath(project)}/backups`, { method: 'POST' })
        reload()
        toast(`Backed up the database, ${size(b.bytes)}`)
      } catch (e) {
        failed(e)
        reload()
      }
    })
  const items: MenuItem[] = [
    { label: 'Change memory', onSelect: () => setResizing(true) },
    { label: 'Restart', icon: <RollBack />, onSelect: restart },
    'separator',
    { label: 'Delete database', icon: <Trash />, danger: true, onSelect: remove },
  ]
  const used = d.container.memory_bytes ?? 0
  return (
    <>
      {data.production && !data.production.connected && (
        <div className="alert info">
          <Info />
          <div>
            <span className="t-small t-strong">Production doesn't have DATABASE_URL yet</span>
            <span className="t-caption t-muted">Redeploy it to connect it.</span>
          </div>
          <Gate allow={change} reason={viewerCant('redeploy')}>
            <button className="btn quiet" onClick={redeploy}>
              Redeploy
            </button>
          </Gate>
        </div>
      )}
      <section className="panel">
        <div className="card-head">
          <h2 className="t-small t-strong">Postgres {d.version}</h2>
          <div className="card-actions">
            <Gate allow={change} reason={viewerCant('back up databases')}>
              <button className="btn quiet" disabled={busy === 'backup' || running} onClick={backUp}>
                {(busy === 'backup' || running) && <Spinner />}
                {busy === 'backup' || running ? 'Backing up…' : 'Back up now'}
              </button>
            </Gate>
            {change && <Menu label="More for the database" items={items} />}
          </div>
        </div>
        <div className="details database-details">
          <div className="details-row">
            <Field label="Status">
              <span className="line">
                <i className={'swatch ' + look.swatch} aria-hidden />
                <span>{look.word}</span>
              </span>
              {d.container.started_at && d.container.state === 'running' && (
                <span className="t-note">
                  Since <When at={d.container.started_at} now={now} lower />
                </span>
              )}
            </Field>
            <Field label="Memory">
              <span>{used ? `${size(used)} of ${memory(d.memory_mb)}` : memory(d.memory_mb)}</span>
              {d.container.oom_killed && <span className="run-why">Ran out of memory</span>}
            </Field>
            <Field label="Disk">
              <span>{d.disk_bytes ? size(d.disk_bytes) : '–'}</span>
              {d.disk_bytes > 0 && <span className="t-note">Production's data is {size(d.data_bytes)}</span>}
            </Field>
            <Field label="Connections">
              <span>
                {d.connections.toLocaleString('en-GB')} of {d.max_connections.toLocaleString('en-GB')}
              </span>
            </Field>
          </div>
          <div className="details-row database-reach">
            <Field label="DATABASE_URL">
              <span className="line">
                <span className="mono">
                  postgres://app:••••••@{d.host}:{d.port}/app
                </span>
                {change && <CopyURL project={project} />}
              </span>
              <span className="t-note">Only {project}'s apps can reach it.</span>
            </Field>
          </div>
          {d.problem && <span className="t-caption run-why">{d.problem}</span>}
        </div>
      </section>
      <Backups project={project} data={data} now={now} reload={reload} restorable />
      <Previews project={project} previews={d.previews} now={now} reload={reload} />
      {resizing && (
        <MemoryDialog
          title="Change the database's memory"
          action="Change memory"
          working="Restarting…"
          choices={data.memory_choices}
          initial={d.memory_mb}
          onClose={() => setResizing(false)}
          run={async (memory_mb) => {
            await api(databasePath(project), { method: 'PUT', body: JSON.stringify({ memory_mb }) })
            setResizing(false)
            reload()
            toast(`The database has ${memory(memory_mb)} of memory now`)
          }}
        />
      )}
    </>
  )
}

// CopyURL copies production's DATABASE_URL, password and all.
function CopyURL({ project }: { project: string }) {
  const [busy, run] = useBusy<'copy'>()
  return (
    <button
      className="icon-btn bare"
      aria-label="Copy DATABASE_URL"
      data-tip="Copy, with the password"
      disabled={busy === 'copy'}
      onClick={() =>
        run('copy', async () => {
          try {
            const r = await api<{ url: string }>(`${databasePath(project)}/url`)
            await copy(r.url, 'DATABASE_URL')
          } catch (e) {
            failed(e)
          }
        })
      }
    >
      {busy === 'copy' ? <Spinner /> : <Copy />}
    </button>
  )
}

function Backups({ project, data, now, reload, restorable }: {
  project: string
  data: DatabaseView
  now: number
  reload: () => void
  restorable: boolean
}) {
  const paged = usePaged(data.backups, PAGE, data.backups.length)
  const hour = String(data.nightly_at).padStart(2, '0')
  return (
    <div className="stack">
      <div className="toolbar">
        <div className="title">
          <h2 className="t-small t-strong">Backups</h2>
          <span className="count">{data.backups.length.toLocaleString('en-GB')}</span>
        </div>
      </div>
      {data.backups.length === 0 ? (
        <Empty title="No backups yet">
          <span className="t-caption t-muted">
            The first is made tonight at {hour}:00 {data.timezone}.
          </span>
        </Empty>
      ) : (
        <>
          <div className="table backups">
            <div className="t-head">
              <span>Made</span>
              <span>Why</span>
              <span>Size</span>
              <span />
            </div>
            {paged.shown.map((b) => (
              <BackupRow key={b.id} b={b} project={project} now={now} reload={reload} restorable={restorable} />
            ))}
          </div>
          <Paging paged={paged} noun="backup" />
          <span className="t-note">
            Nightly at {hour}:00 {data.timezone}. The last 7 are kept.
          </span>
        </>
      )}
    </div>
  )
}

function BackupRow({ b, project, now, reload, restorable }: {
  b: DatabaseBackup
  project: string
  now: number
  reload: () => void
  restorable: boolean
}) {
  const { change } = useRole()
  const base = `${databasePath(project)}/backups/${b.id}`
  const restore = () =>
    confirm({
      title: 'Restore this backup?',
      body: (
        <p>
          Production's database goes back to {new Date(b.started_at).toLocaleString('en-GB', { dateStyle: 'long', timeStyle: 'short' })}. What's there now is
          backed up first.
        </p>
      ),
      action: 'Restore',
      working: 'Restoring…',
      run: async () => {
        await api(`${base}/restore`, { method: 'POST' })
        reload()
      },
    }).then((ok) => ok && toast('Restored the database'))
  const done = b.state === 'done'
  const items: MenuItem[] = [
    { label: 'Download', onSelect: () => (window.location.href = `${base}/file`) },
    ...(restorable ? (['separator', { label: 'Restore', icon: <RollBack />, onSelect: restore }] as MenuItem[]) : []),
  ]
  return (
    <div className="t-row">
      <span className="backup-made">
        <When at={b.started_at} now={now} />
        {b.made_by && <span className="t-note">{b.made_by}</span>}
      </span>
      <span className="t-muted">
        {b.state === 'running' ? (
          <span className="run-status">
            <Spinner className="building" />
            Backing up
          </span>
        ) : b.state === 'failed' ? (
          <span className="run-status run-why" data-tip={b.error}>
            <i className="swatch failed" aria-hidden />
            {reasons[b.reason]}, failed
          </span>
        ) : (
          reasons[b.reason]
        )}
      </span>
      <span className="t-muted">{done ? size(b.bytes) : ''}</span>
      <span className="row-more">{change && done && <Menu label="Actions for this backup" items={items} className="icon-btn bare" />}</span>
    </div>
  )
}

function Previews({ project, previews, now, reload }: { project: string; previews: PreviewDatabase[]; now: number; reload: () => void }) {
  const { change } = useRole()
  const paged = usePaged(previews, PAGE, previews.length)
  const label = (p: PreviewDatabase) => p.branch || 'pier deploy previews'
  const remove = (p: PreviewDatabase) =>
    confirm({
      title: `Delete the database of ${label(p)}?`,
      body: <p>Its next preview gets a new, empty one.</p>,
      action: 'Delete',
      working: 'Deleting…',
      danger: true,
      run: async () => {
        await api(`${databasePath(project)}/previews/${encodeURIComponent(p.name)}`, { method: 'DELETE' })
        reload()
      },
    }).then((ok) => ok && toast(`Deleted the database of ${label(p)}`))
  if (previews.length === 0) return null
  return (
    <div className="stack">
      <div className="toolbar">
        <div className="title">
          <h2 className="t-small t-strong">Preview databases</h2>
          <span className="count">{previews.length.toLocaleString('en-GB')}</span>
        </div>
      </div>
      <div className="table preview-dbs">
        <div className="t-head">
          <span>Branch</span>
          <span>Database</span>
          <span>Size</span>
          <span>Made</span>
          <span />
        </div>
        {paged.shown.map((p) => (
          <div className="t-row" key={p.name}>
            <span className="line">
              <i className="swatch building" aria-hidden />
              <span className={p.branch ? 'mono' : ''}>{label(p)}</span>
            </span>
            <span className="mono t-muted">{p.name}</span>
            <span className="t-muted">{p.bytes ? size(p.bytes) : ''}</span>
            <When at={p.created_at} now={now} className="t-muted" />
            <span className="row-more">
              {change && <Menu label={`Actions for ${p.name}`} items={[{ label: 'Delete', icon: <Trash />, danger: true, onSelect: () => remove(p) }]} className="icon-btn bare" />}
            </span>
          </div>
        ))}
      </div>
      <Paging paged={paged} noun="preview database" />
      <span className="t-note">Each branch gets its own, deleted with the branch.</span>
    </div>
  )
}

// MemoryDialog picks a database's memory limit.
function MemoryDialog({ title, action, working, choices, initial, onClose, run }: {
  title: string
  action: string
  working: string
  choices: number[]
  initial: number
  onClose: () => void
  run: (memoryMB: number) => Promise<void>
}) {
  const [value, setValue] = useState(initial)
  const [busy, setBusy] = useState(false)
  const [problem, setProblem] = useState('')
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setProblem('')
    try {
      await run(value)
    } catch (err) {
      setProblem(err instanceof Error ? err.message : "Can't reach pierd.")
      setBusy(false)
    }
  }
  return (
    <Dialog
      wide
      title={title}
      busy={busy}
      onClose={onClose}
      onSubmit={submit}
      footer={
        <>
          {problem && (
            <span className="field-message grow" role="alert">
              {problem}
            </span>
          )}
          <button className="btn quiet medium" type="button" disabled={busy} onClick={onClose}>
            Cancel
          </button>
          <button className="btn primary medium" type="submit" disabled={busy}>
            {busy && <Spinner />}
            {busy ? working : action}
          </button>
        </>
      }
    >
      <div className="field">
        <span className="field-label">Memory</span>
        <div className="seg" role="radiogroup" aria-label="Memory">
          {choices.map((n) => (
            <button key={n} type="button" role="radio" aria-checked={value === n} className={value === n ? 'on' : ''} onClick={() => setValue(n)}>
              {memory(n)}
            </button>
          ))}
        </div>
        <span className="t-note">256 MB suits most apps.</span>
      </div>
    </Dialog>
  )
}
