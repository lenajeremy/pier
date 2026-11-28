import { useState, type FormEvent } from 'react'
import { api, type DatabaseBackup, type DatabaseStatus, type DatabaseView, type Environment, type ExternalURL, type ProjectDatabase } from '../lib/api'
import { failed } from '../lib/actions'
import { useData } from '../lib/data'
import { size } from '../lib/format'
import { navigate } from '../lib/router'
import { Copy, Info, Pencil, RollBack, Trash, Visit } from '../components/Icons'
import { EnvPicker } from '../components/EnvPicker'
import { Sheet } from '../components/Sheet'
import { confirm, Dialog } from '../components/Dialog'
import { Menu, type MenuItem } from '../components/Menu'
import { Spinner, useBusy } from '../components/Spinner'
import { copy, toast } from '../components/Toast'
import { Badge, Empty, Paging, Problem, Skeleton, usePaged, When } from '../components/ui'
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
  const [db, setDb] = useState('')
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
          <span className="t-caption t-muted">Postgres for {project} alone, with a database for production and one for previews.</span>
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

  const swatchOf = (x: ProjectDatabase) => (x.environments.includes('production') ? 'live' : x.environments.includes('preview') ? 'building' : '')
  const databases = d.databases.map((x) => ({ name: x.name, swatch: swatchOf(x), production: x.environments.includes('production') }))
  const chosen = databases.find((x) => x.name === db) ?? databases.find((x) => x.production) ?? databases[0] ?? { name: '', swatch: '', production: false }
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
            items={databases.map((x) => ({ label: x.name, onSelect: () => setDb(x.name) }))}
          >
            <i className={'swatch ' + chosen.swatch} aria-hidden />
            <span className="mono">{chosen.name}</span>
          </Menu>
        )}
      </div>
      {view === 'overview' ? (
        <Overview project={project} data={data} d={d} now={now} reload={reload} onBrowse={(name) => (setDb(name), setView('data'))} />
      ) : view === 'data' ? (
        <DataView project={project} db={chosen.name} onQuery={(sql) => (setDraft(sql), setView('query'))} />
      ) : (
        <QueryView project={project} db={chosen.name} role={chosen.name} draft={draft} key={chosen.name} />
      )}
    </main>
  )
}

function Overview({ project, data, d, now, reload, onBrowse }: {
  project: string
  data: DatabaseView
  d: DatabaseStatus
  now: number
  reload: () => void
  onBrowse: (name: string) => void
}) {
  const [resizing, setResizing] = useState(false)
  const [busy, run] = useBusy<'backup'>()
  const { change } = useRole()
  const look = state(d.container)
  const running = data.backups.some((b) => b.state === 'running')
  const redeploy = () =>
    confirm({
      title: 'Redeploy production?',
      body: <p>The new build gets its database. It goes live once it works.</p>,
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
      title: `Delete ${project}'s Postgres?`,
      body: <p>Every database on it is deleted. A last backup of each is kept.</p>,
      action: 'Delete Postgres',
      working: 'Backing up and deleting…',
      danger: true,
      run: async () => {
        await api(databasePath(project), { method: 'DELETE' })
        reload()
      },
    }).then((ok) => ok && toast("Deleted the project's Postgres"))
  const backUp = () =>
    run('backup', async () => {
      try {
        const b = await api<{ backups: DatabaseBackup[]; bytes: number }>(`${databasePath(project)}/backups`, { method: 'POST' })
        reload()
        toast(b.backups.length === 1 ? `Backed up ${b.backups[0].database}, ${size(b.bytes)}` : `Backed up ${b.backups.length} databases, ${size(b.bytes)}`)
      } catch (e) {
        failed(e)
        reload()
      }
    })
  const items: MenuItem[] = [
    { label: 'Change memory', onSelect: () => setResizing(true) },
    { label: 'Restart', icon: <RollBack />, onSelect: restart },
    'separator',
    { label: 'Delete Postgres', icon: <Trash />, danger: true, onSelect: remove },
  ]
  const used = d.container.memory_bytes ?? 0
  return (
    <>
      {data.production && !data.production.connected && (
        <div className="alert info">
          <Info />
          <div>
            <span className="t-small t-strong">Production doesn't have its database yet</span>
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
            {change && <Menu label="More for Postgres" items={items} />}
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
              {d.disk_bytes > 0 && <span className="t-note">Data is {size(d.data_bytes)}</span>}
            </Field>
            <Field label="Connections">
              <span>
                {d.connections.toLocaleString('en-GB')} of {d.max_connections.toLocaleString('en-GB')}
              </span>
            </Field>
          </div>
          {d.problem && <span className="t-caption run-why">{d.problem}</span>}
        </div>
      </section>
      <Databases project={project} data={data} d={d} now={now} reload={reload} onBrowse={onBrowse} />
      <Backups project={project} data={data} now={now} reload={reload} restorable />
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
            The first are made tonight at {hour}:00 {data.timezone}.
          </span>
        </Empty>
      ) : (
        <>
          <div className="table backups">
            <div className="t-head">
              <span>Made</span>
              <span>Database</span>
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
            Each database nightly at {hour}:00 {data.timezone}. The last 7 of each are kept.
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
          <span className="mono">{b.database}</span> goes back to {new Date(b.started_at).toLocaleString('en-GB', { dateStyle: 'long', timeStyle: 'short' })}. What's there now is
          backed up first.
        </p>
      ),
      action: 'Restore',
      working: 'Restoring…',
      run: async () => {
        await api(`${base}/restore`, { method: 'POST' })
        reload()
      },
    }).then((ok) => ok && toast(`Restored ${b.database}`))
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
      <span className="mono t-muted">{b.database}</span>
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

// clash finds another database that already gives one of these
// environments the same variable.
const clash = (all: ProjectDatabase[], name: string, variable: string, envs: Environment[]) =>
  all.find((o) => o.name !== name && o.variable === variable && o.environments.some((e) => envs.includes(e)))

// Databases lists the server's databases: what each serves, the variable
// it's given as, and its size.
function Databases({ project, data, d, now, reload, onBrowse }: {
  project: string
  data: DatabaseView
  d: DatabaseStatus
  now: number
  reload: () => void
  onBrowse: (name: string) => void
}) {
  const { change } = useRole()
  const [editing, setEditing] = useState<ProjectDatabase | 'new'>()
  const [made, setMade] = useState<{ database: string; url: string }>()
  const [open, setOpen] = useState<string>()
  const base = `${databasePath(project)}/databases`
  const copyURL = async (x: ProjectDatabase) => {
    try {
      const r = await api<{ url: string }>(`${databasePath(project)}/url?db=${encodeURIComponent(x.name)}`)
      await copy(r.url, `${x.name}'s URL`)
    } catch (e) {
      failed(e)
    }
  }
  const external = async (x: ProjectDatabase) => {
    try {
      const r = await api<{ url: string }>(`${base}/${encodeURIComponent(x.name)}/externals`, { method: 'POST' })
      setMade({ database: x.name, url: r.url })
      reload()
    } catch (e) {
      failed(e)
    }
  }
  const remove = (x: ProjectDatabase) =>
    confirm({
      title: `Delete ${x.name}?`,
      body: <p>Its data is deleted. A last backup is kept.</p>,
      action: 'Delete database',
      working: 'Backing up and deleting…',
      danger: true,
      run: async () => {
        await api(`${base}/${encodeURIComponent(x.name)}`, { method: 'DELETE' })
        reload()
      },
    }).then((ok) => {
      if (ok) toast(`Deleted ${x.name}`)
      return ok
    })
  return (
    <div className="stack">
      <div className="toolbar">
        <div className="title">
          <h2 className="t-small t-strong">Databases</h2>
          <span className="count">{d.databases.length.toLocaleString('en-GB')}</span>
        </div>
        <Gate allow={change} reason={viewerCant('add databases')}>
          <button className="btn quiet" onClick={() => setEditing('new')}>
            Add database
          </button>
        </Gate>
      </div>
      <div className="table project-dbs">
        <div className="t-head">
          <span>Database</span>
          <span>Serves</span>
          <span>Variable</span>
          <span className="num">Size</span>
          <span />
        </div>
        {d.databases.map((x) => {
          const items: MenuItem[] = [
            { label: 'Open', onSelect: () => setOpen(x.name) },
            { label: 'Browse data', onSelect: () => onBrowse(x.name) },
            { label: 'Copy URL', icon: <Copy />, onSelect: () => copyURL(x) },
            { label: 'Make an external URL', icon: <Visit />, onSelect: () => external(x), disabled: !data.external },
            { label: 'Change', icon: <Pencil />, onSelect: () => setEditing(x) },
            'separator',
            { label: 'Delete database', icon: <Trash />, danger: true, onSelect: () => remove(x) },
          ]
          return (
            <div key={x.name} className="t-row clickable" onClick={(e) => !(e.target as HTMLElement).closest('button, a, [role="menu"]') && setOpen(x.name)}>
              <span className="mono t-strong" data-tip-cut>
                {x.name}
              </span>
              <span className="scopes">
                {x.environments.length ? x.environments.map((e) => <Badge key={e} role={e} />) : <span className="t-muted">Nothing</span>}
              </span>
              <span className="mono t-muted" data-tip-cut>
                {x.environments.length ? x.variable : ''}
              </span>
              <span className="num t-muted">{x.bytes ? size(x.bytes) : '–'}</span>
              <span className="row-more">{change && <Menu label={`Actions for ${x.name}`} items={items} className="icon-btn bare" />}</span>
            </div>
          )
        })}
      </div>
      <span className="t-note">Only {project}'s apps reach them. A change reaches a deploy when it starts.</span>
      {open && d.databases.some((x) => x.name === open) && (
        <DatabaseSheet
          project={project}
          data={data}
          x={d.databases.find((x) => x.name === open)!}
          index={d.databases.findIndex((x) => x.name === open)}
          count={d.databases.length}
          now={now}
          onMove={(step) => {
            const i = d.databases.findIndex((x) => x.name === open) + step
            if (i >= 0 && i < d.databases.length) setOpen(d.databases[i].name)
          }}
          onClose={() => setOpen(undefined)}
          onBrowse={() => (setOpen(undefined), onBrowse(open))}
          onChange={() => setEditing(d.databases.find((x) => x.name === open))}
          onDelete={() => remove(d.databases.find((x) => x.name === open)!).then((ok) => ok && setOpen(undefined))}
          onExternal={() => external(d.databases.find((x) => x.name === open)!)}
          reload={reload}
        />
      )}
      {editing && (
        <DatabaseDialog
          project={project}
          all={d.databases}
          editing={editing === 'new' ? undefined : editing}
          onClose={() => setEditing(undefined)}
          onDone={(msg) => {
            setEditing(undefined)
            reload()
            toast(msg)
          }}
        />
      )}
      {made && (
        <Dialog
          wide
          title={
            <>
              An external URL for <span className="mono">{made.database}</span>
            </>
          }
          onClose={() => setMade(undefined)}
          footer={
            <>
              <button className="btn quiet medium" type="button" onClick={() => copy(made.url, 'the external URL')}>
                Copy
              </button>
              <button className="btn primary medium" type="button" onClick={() => setMade(undefined)}>
                Done
              </button>
            </>
          }
        >
          <pre className="external-url mono">{made.url}</pre>
          <span className="t-note">It reaches {made.database} from anywhere, over TLS. Revoke it any time.</span>
        </Dialog>
      )}
    </div>
  )
}

function DatabaseDialog({ project, all, editing, onClose, onDone }: {
  project: string
  all: ProjectDatabase[]
  editing?: ProjectDatabase
  onClose: () => void
  onDone: (msg: string) => void
}) {
  const [name, setName] = useState(editing?.name ?? '')
  const [envs, setEnvs] = useState<Environment[]>(editing?.environments ?? [])
  const [variable, setVariable] = useState(editing?.variable ?? 'DATABASE_URL')
  const [busy, setBusy] = useState(false)
  const [problem, setProblem] = useState<{ field?: string; message: string }>()
  const clashing = clash(all, name, variable, envs)
  const taken = !editing && all.some((o) => o.name === name)
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setProblem(undefined)
    try {
      const base = `${databasePath(project)}/databases`
      if (editing) {
        await api(`${base}/${encodeURIComponent(editing.name)}`, { method: 'PUT', body: JSON.stringify({ environments: envs, variable }) })
        onDone(`Saved ${editing.name}. Redeploy to pass it on`)
      } else {
        await api(base, { method: 'POST', body: JSON.stringify({ name, environments: envs, variable }) })
        onDone(`Added ${name}`)
      }
    } catch (err) {
      const body = (err as { body?: { field?: string; error?: string } }).body
      setProblem({ field: body?.field, message: body?.error ?? (err instanceof Error ? err.message : "Can't reach pierd.") })
      setBusy(false)
    }
  }
  return (
    <Dialog
      wide
      title={
        editing ? (
          <>
            Change <span className="mono">{editing.name}</span>
          </>
        ) : (
          'Add a database'
        )
      }
      busy={busy}
      onClose={onClose}
      onSubmit={submit}
      footer={
        <>
          {problem && !problem.field && (
            <span className="field-message grow" role="alert">
              {problem.message}
            </span>
          )}
          <button className="btn quiet medium" type="button" disabled={busy} onClick={onClose}>
            Cancel
          </button>
          <button className="btn primary medium" type="submit" disabled={busy || !name || !variable || !!clashing || taken}>
            {busy && <Spinner />}
            {busy ? (editing ? 'Saving…' : 'Adding…') : editing ? 'Save' : 'Add database'}
          </button>
        </>
      }
    >
      {!editing && (
        <label className={'field' + (problem?.field === 'name' || taken ? ' error' : '')}>
          <span className="field-label">Name</span>
          <input
            className="input mono"
            value={name}
            onChange={(e) => (setName(e.target.value.toLowerCase().replace(/[^a-z0-9_]/g, '')), setProblem(undefined))}
            placeholder="analytics"
            maxLength={40}
            spellCheck={false}
            autoCapitalize="off"
          />
          {(problem?.field === 'name' || taken) && (
            <span className="field-message" role="alert">
              {taken ? `There's one called ${name} already.` : problem?.message}
            </span>
          )}
        </label>
      )}
      <div className="field">
        <span className="field-label">Serves</span>
        <EnvPicker value={envs} onChange={setEnvs} />
      </div>
      <label className={'field' + (clashing || problem?.field === 'variable' ? ' error' : '')}>
        <span className="field-label">Variable</span>
        <input
          className="input mono"
          value={variable}
          onChange={(e) => (setVariable(e.target.value.toUpperCase().replace(/[^A-Z0-9_]/g, '')), setProblem(undefined))}
          maxLength={64}
          spellCheck={false}
          autoCapitalize="off"
        />
        {(clashing || problem?.field === 'variable') && (
          <span className="field-message" role="alert">
            {clashing ? `${clashing.name} gives ${clashing.environments.join(' and ')} ${variable} already.` : problem?.message}
          </span>
        )}
      </label>
    </Dialog>
  )
}

// DatabaseSheet is one database, opened: what it serves, how apps reach
// it, and the URLs services outside Pier reach it by.
function DatabaseSheet({ project, data, x, index, count, now, onMove, onClose, onBrowse, onChange, onDelete, onExternal, reload }: {
  project: string
  data: DatabaseView
  x: ProjectDatabase
  index: number
  count: number
  now: number
  onMove: (d: number) => void
  onClose: () => void
  onBrowse: () => void
  onChange: () => void
  onDelete: () => void
  onExternal: () => void
  reload: () => void
}) {
  const { change } = useRole()
  const ext = data.externals.find((e) => e.database === x.name)
  const path = (e: ExternalURL) => `${databasePath(project)}/externals/${encodeURIComponent(e.role)}`
  const copyURL = async (u: string, what: string) => {
    try {
      const r = await api<{ url: string }>(u)
      await copy(r.url, what)
    } catch (err) {
      failed(err)
    }
  }
  const revoke = (e: ExternalURL) =>
    confirm({
      title: 'Revoke this external URL?',
      body: (
        <p>
          Services using <span className="mono">{e.role}</span> are disconnected now.
        </p>
      ),
      action: 'Revoke',
      working: 'Revoking…',
      danger: true,
      run: async () => {
        await api(path(e), { method: 'DELETE' })
        reload()
      },
    }).then((ok) => ok && toast('Revoked the external URL'))
  return (
    <Sheet
      label="Database"
      onClose={onClose}
      onMove={onMove}
      first={index === 0}
      last={index === count - 1}
      title={
        <>
          <span className="t-note">Database</span>
          <span className="t-small t-strong db-sheet-id mono">{x.name}</span>
        </>
      }
      foot={
        <>
          <button className="btn quiet" onClick={onBrowse} disabled={!change}>
            Browse data
          </button>
          {change && (
            <button className="btn quiet" onClick={onChange}>
              Change
            </button>
          )}
          {change && (
            <button className="btn quiet danger push" onClick={onDelete}>
              Delete
            </button>
          )}
        </>
      }
    >
      <div className="db-sheet-fields sheet-sans">
        <div className="db-fields">
          <div className="db-field">
            <span className="t-quiet">Serves</span>
            <span className="scopes">{x.environments.length ? x.environments.map((e) => <Badge key={e} role={e} />) : 'Nothing'}</span>
          </div>
          <div className="db-field">
            <span className="t-quiet">Variable</span>
            <span className="mono">{x.environments.length ? x.variable : '–'}</span>
          </div>
          <div className="db-field">
            <span className="t-quiet">Size</span>
            <span>{x.bytes ? size(x.bytes) : '–'}</span>
          </div>
          <div className="db-field">
            <span className="t-quiet">Made</span>
            <span>
              <When at={x.created_at} now={now} />
              {x.created_by && <span className="t-note"> by {x.created_by}</span>}
            </span>
          </div>
          <div className="db-field">
            <span className="t-quiet">URL</span>
            <span className="line file-url">
              <span className="mono" data-tip-cut>
                postgres://{x.name}:••••@postgres:5432/{x.name}
              </span>
              {change && (
                <button className="icon-btn bare small" aria-label="Copy the URL" data-tip="Copy, with the password" onClick={() => copyURL(`${databasePath(project)}/url?db=${encodeURIComponent(x.name)}`, `${x.name}'s URL`)}>
                  <Copy />
                </button>
              )}
            </span>
          </div>
          <div className="db-field">
            <span className="t-quiet">External URL</span>
            {ext ? (
              <span className="line file-url">
                <span className="mono" data-tip-cut>
                  postgres://{ext.role}:••••@{data.external}/{x.name}
                </span>
                {change && (
                  <>
                    <button className="icon-btn bare small" aria-label="Copy the external URL" data-tip="Copy, with the password" onClick={() => copyURL(path(ext), 'the external URL')}>
                      <Copy />
                    </button>
                    <Menu
                      label="More for the external URL"
                      className="icon-btn bare small"
                      items={[{ label: 'Revoke', icon: <Trash />, danger: true, onSelect: () => revoke(ext) }]}
                    />
                  </>
                )}
              </span>
            ) : !data.external ? (
              <span className="t-muted">This server doesn't make them</span>
            ) : (
              <span className="line file-url">
                <span className="t-muted">Off</span>
                {change && (
                  <button className="btn quiet small" onClick={onExternal}>
                    Make one
                  </button>
                )}
              </span>
            )}
          </div>
        </div>
      </div>
    </Sheet>
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
