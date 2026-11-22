import { useEffect, useMemo, useState, type FormEvent } from 'react'
import { api, ApiError, type Job, type JobRun } from '../lib/api'
import { failed } from '../lib/actions'
import { useData } from '../lib/data'
import { exact, until } from '../lib/format'
import { Link, navigate } from '../lib/router'
import { Info, Pencil, Play, Trash } from '../components/Icons'
import { confirm, Dialog } from '../components/Dialog'
import { Menu, type MenuItem } from '../components/Menu'
import { Spinner } from '../components/Spinner'
import { toast } from '../components/Toast'
import { Empty, Paging, Problem, SearchField, Skeleton, usePaged, When } from '../components/ui'
import { Gate, useRole, viewerCant } from '../lib/role'

type Data = {
  jobs: Job[]
  production: { id: string; server: boolean; created_at: string } | null
  secret_stale: boolean // production was built before it had the cron secret
  secret_name: string
}
const PAGE = 20

export const jobsPath = (project: string) => `/api/projects/${encodeURIComponent(project)}/jobs`

// RunStatus is a run's state as a swatch and a word.
export function RunStatus({ run }: { run: Pick<JobRun, 'status'> }) {
  if (run.status === 'running') {
    return (
      <span className="run-status">
        <Spinner className="building" />
        Running
      </span>
    )
  }
  const look = { succeeded: ['done', 'Succeeded'], failed: ['failed', 'Failed'], skipped: ['', 'Skipped'] }[run.status]
  return (
    <span className="run-status">
      <i className={'swatch ' + look[0]} aria-hidden />
      {look[1]}
    </span>
  )
}

// Until is a time to come in words, with the exact time on hover.
export function Until({ at, now, className }: { at: string; now: number; className?: string }) {
  return (
    <time className={className} dateTime={at} data-tip={exact(at)} data-tip-mono tabIndex={-1}>
      {until(at, now)}
    </time>
  )
}

// What a job does, in a line: its command, or its method and path.
export const what = (j: Pick<Job, 'kind' | 'command' | 'method' | 'path'>) => (j.kind === 'command' ? j.command : `${j.method} ${j.path}`)

// runNow starts a run, says so, and opens it.
export async function runNow(project: string, j: Job, done?: () => void) {
  try {
    const r = await api<{ run: number }>(`${jobsPath(project)}/${j.id}/run`, { method: 'POST' })
    done?.()
    toast(`Started ${j.name}`)
    navigate(`/projects/${project}/jobs/${j.id}/runs/${r.run}`)
  } catch (e) {
    failed(e)
  }
}

// toggle turns a job's schedule off or on.
export async function toggle(project: string, j: Job, done: () => void) {
  try {
    await api(`${jobsPath(project)}/${j.id}`, { method: 'PUT', body: JSON.stringify(request(j, { enabled: !j.enabled })) })
    done()
    toast(j.enabled ? `${j.name} won't run on its schedule until you turn it on` : `${j.name} runs on its schedule again`)
  } catch (e) {
    failed(e)
  }
}

// remove asks, then deletes a job and its runs.
export function remove(project: string, j: Job, done: () => void) {
  return confirm({
    title: `Delete ${j.name}?`,
    body: <p>It stops running, and its runs and their output are deleted. Nothing in production changes.</p>,
    action: 'Delete',
    working: 'Deleting…',
    danger: true,
    run: async () => {
      await api(`${jobsPath(project)}/${j.id}`, { method: 'DELETE' })
      done()
    },
  }).then((ok) => ok && toast(`Deleted ${j.name}`))
}

const request = (j: Job, change: Partial<Job> = {}) => {
  const n = { ...j, ...change }
  return {
    name: n.name, kind: n.kind, schedule: n.schedule, timezone: n.timezone, command: n.command, method: n.method,
    path: n.path, timeout_minutes: Math.round(n.timeout_s / 60), enabled: n.enabled,
  }
}

// Jobs is the project's Jobs tab: what runs on a schedule, and how each
// last went.
export function Jobs({ project, now }: { project: string; now: number }) {
  const { data, error, reload } = useData<Data>(jobsPath(project), () => true, (d) => (d.jobs.some((j) => j.running) ? 2000 : 15_000))
  const [query, setQuery] = useState('')
  const [editing, setEditing] = useState<Job | 'new' | null>(null)
  const { change } = useRole()
  const shown = useMemo(() => {
    const q = query.trim().toLowerCase()
    return (data?.jobs ?? []).filter((j) => !q || [j.name, what(j) ?? '', j.description].some((v) => v.toLowerCase().includes(q)))
  }, [data, query])
  const paged = usePaged(shown, PAGE, query)

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
          <Skeleton rows={4} />
        </div>
      </main>
    )
  }

  const redeploy = () =>
    confirm({
      title: 'Redeploy production?',
      body: (
        <p>
          Pier builds production's source again. The new build has <span className="mono">{data.secret_name}</span>, so the app
          can check that a job's request comes from Pier. When it works, it goes live in place of what's serving.
        </p>
      ),
      action: 'Redeploy',
      working: 'Starting the build…',
      run: async () => {
        const r = await api<{ id?: string }>(`/api/projects/${encodeURIComponent(project)}/redeploy`, { method: 'POST', body: '{}' })
        if (r.id) navigate(`/projects/${project}/deployments/${r.id}`)
      },
    })

  const add = (
    <Gate allow={change} reason={viewerCant('add jobs')}>
      <button className="btn primary" onClick={() => setEditing('new')}>
        Add job
      </button>
    </Gate>
  )
  return (
    <main className="main">
      {data.secret_stale && (
        <div className="alert info">
          <Info />
          <div>
            <span className="t-small t-strong">Production doesn't have {data.secret_name} yet</span>
            <span className="t-caption t-muted">
              It was built before Pier made the secret its jobs send. Redeploy it so the app can check the{' '}
              <span className="mono">Authorization</span> header of a job's request.
            </span>
          </div>
          <Gate allow={change} reason={viewerCant('redeploy')}>
            <button className="btn quiet" onClick={redeploy}>
              Redeploy
            </button>
          </Gate>
        </div>
      )}
      <div className="stack">
        <div className="toolbar">
          <div className="title">
            <h2 className="t-small t-strong">Jobs</h2>
            <span className="count">{data.jobs.length.toLocaleString('en-GB')}</span>
          </div>
          {data.jobs.length > 5 && <SearchField value={query} onChange={setQuery} placeholder="Search jobs" hotkey="/" width={260} />}
          {data.jobs.length > 0 && add}
        </div>
        {data.jobs.length === 0 ? (
          <Empty title="No scheduled jobs yet">
            <span className="t-caption t-muted" style={{ maxWidth: 540 }}>
              A job runs on a schedule you set, like every night at 03:00. It runs a command in a fresh container from
              production's image, or calls a path on production, and keeps each run's output.
            </span>
            {add}
          </Empty>
        ) : (
          <div className="table jobs">
            <div className="t-head">
              <span>Job</span>
              <span>Schedule</span>
              <span>Last run</span>
              <span>Next run</span>
              <span />
            </div>
            {paged.shown.map((j) => (
              <JobRow key={j.id} j={j} project={project} now={now} reload={reload} onEdit={() => setEditing(j)} />
            ))}
            {shown.length === 0 && (
              <div className="t-empty">
                <span className="t-caption t-muted">No job matches “{query.trim()}”.</span>
                <button className="btn quiet small" onClick={() => setQuery('')}>
                  Show all
                </button>
              </div>
            )}
          </div>
        )}
        <Paging paged={paged} noun="job" />
      </div>
      {editing && (
        <JobDialog
          project={project}
          job={editing === 'new' ? null : editing}
          staticSite={!!data.production && !data.production.server}
          secretName={data.secret_name}
          onClose={() => setEditing(null)}
          onDone={(j) => {
            setEditing(null)
            reload()
            toast(editing === 'new' ? `Added ${j.name}` : `Saved ${j.name}`)
          }}
        />
      )}
    </main>
  )
}

function JobRow({ j, project, now, reload, onEdit }: { j: Job; project: string; now: number; reload: () => void; onEdit: () => void }) {
  const { change } = useRole()
  const page = `/projects/${project}/jobs/${j.id}`
  const items: MenuItem[] = [
    { label: 'Run now', icon: <Play />, disabled: j.running, onSelect: () => runNow(project, j, reload) },
    { label: 'Edit', icon: <Pencil />, onSelect: onEdit },
    { label: j.enabled ? 'Turn off its schedule' : 'Turn on its schedule', onSelect: () => toggle(project, j, reload) },
    'separator',
    { label: 'Delete', icon: <Trash />, danger: true, disabled: j.running, onSelect: () => remove(project, j, reload) },
  ]
  const last = j.running ? { status: 'running' as const } : j.last_run
  return (
    <div className="t-row clickable grow" onClick={() => navigate(page)}>
      <span className="job-name">
        <Link to={page} onClick={(e) => e.stopPropagation()}>
          {j.name}
        </Link>
        <span className="mono t-note">{what(j)}</span>
      </span>
      <span className="t-muted" data-tip={`${j.schedule}${j.timezone === 'UTC' ? '' : `, ${j.timezone}`}`} data-tip-mono>
        {j.description}
      </span>
      <span className="job-last">
        {last ? (
          <>
            <RunStatus run={last} />
            {j.last_run && !j.running && <When at={j.last_run.started_at} now={now} className="t-note" />}
          </>
        ) : (
          <span className="t-quiet">Not run yet</span>
        )}
      </span>
      <span className="t-muted">{!j.enabled ? <span className="t-quiet">Off</span> : j.next_run ? <Until at={j.next_run} now={now} /> : 'Never'}</span>
      <span className="row-more" onClick={(e) => e.stopPropagation()}>
        {/* A viewer's row opens the job; every action here changes something. */}
        {change && <Menu label={`Actions for ${j.name}`} items={items} className="icon-btn bare" />}
      </span>
    </div>
  )
}

type Fault = { text: string; field?: string } | null
const fault = (e: unknown): Fault =>
  e instanceof ApiError ? { text: e.message, field: String(e.body.field ?? '') } : { text: "Can't reach pierd." }

const zones = (() => {
  try {
    return ['UTC', ...Intl.supportedValuesOf('timeZone').filter((z) => z !== 'UTC')]
  } catch {
    return ['UTC']
  }
})()

type Preview = { description: string; next: string[] } | { error: string; field?: string }

// useSchedule says when a schedule runs in words, as it's typed, from
// pierd, which reads it the same way when it runs.
function useSchedule(schedule: string, timezone: string): Preview | null {
  const [preview, setPreview] = useState<Preview | null>(null)
  useEffect(() => {
    if (!schedule.trim()) {
      setPreview(null)
      return
    }
    let live = true
    const t = window.setTimeout(async () => {
      try {
        const r = await api<{ description: string; next: string[] }>('/api/jobs/schedule', {
          method: 'POST',
          body: JSON.stringify({ schedule, timezone }),
        })
        if (live) setPreview(r)
      } catch (e) {
        if (live) setPreview({ error: e instanceof Error ? e.message : "Can't reach pierd.", field: e instanceof ApiError ? String(e.body.field ?? '') : '' })
      }
    }, 250)
    return () => {
      live = false
      window.clearTimeout(t)
    }
  }, [schedule, timezone])
  return preview
}

// JobDialog adds a job, or changes one.
export function JobDialog({ project, job, staticSite, secretName, onClose, onDone }: {
  project: string
  job: Job | null
  staticSite: boolean // production has no image, so it can't run a command
  secretName: string
  onClose: () => void
  onDone: (j: Job) => void
}) {
  const [name, setName] = useState(job?.name ?? '')
  const [kind, setKind] = useState<Job['kind']>(job?.kind ?? (staticSite ? 'path' : 'command'))
  const [command, setCommand] = useState(job?.command ?? '')
  const [method, setMethod] = useState<'GET' | 'POST'>(job?.method ?? 'GET')
  const [path, setPath] = useState(job?.path ?? '')
  const [schedule, setSchedule] = useState(job?.schedule ?? '')
  const [timezone, setTimezone] = useState(job?.timezone ?? 'UTC')
  const [timeout, setTimeout_] = useState(String(job ? Math.round(job.timeout_s / 60) : 15))
  const [enabled, setEnabled] = useState(job?.enabled ?? true)
  const [problem, setProblem] = useState<Fault>(null)
  const [busy, setBusy] = useState(false)
  const preview = useSchedule(schedule, timezone)

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setProblem(null)
    try {
      const body = JSON.stringify({ name, kind, command, method, path, schedule, timezone, timeout_minutes: Number(timeout), enabled })
      const r = await api<{ job: Job }>(job ? `${jobsPath(project)}/${job.id}` : jobsPath(project), { method: job ? 'PUT' : 'POST', body })
      onDone(r.job)
    } catch (err) {
      setProblem(fault(err))
      setBusy(false)
    }
  }
  const field = (k: string) => 'field' + (problem?.field === k ? ' error' : '')
  const help = (k: string, text: React.ReactNode) =>
    problem?.field === k ? (
      <span className="field-message" role="alert">
        {problem.text}
      </span>
    ) : (
      <span className="t-note">{text}</span>
    )
  const typed = (set: (v: string) => void) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => {
    set(e.target.value)
    setProblem(null)
  }
  const bad = preview && 'error' in preview
  return (
    <Dialog
      wide
      title={job ? `Change ${job.name}` : 'Add a job'}
      busy={busy}
      onClose={onClose}
      onSubmit={submit}
      footer={
        <>
          {problem && !problem.field && (
            <span className="field-message grow" role="alert">
              {problem.text}
            </span>
          )}
          <button className="btn quiet medium" type="button" disabled={busy} onClick={onClose}>
            Cancel
          </button>
          <button className="btn primary medium" type="submit" disabled={busy || !name.trim() || !schedule.trim()}>
            {busy && <Spinner />}
            {busy ? 'Saving…' : job ? 'Save' : 'Add job'}
          </button>
        </>
      }
    >
      <label className={field('name')}>
        <span className="field-label">Name</span>
        <input className="input mono" placeholder="nightly-cleanup" value={name} onChange={typed(setName)} spellCheck={false} autoCapitalize="off" autoComplete="off" />
        {help('name', <>Letters, numbers, dots and dashes. <span className="mono">pier jobs run</span> finds it by this name.</>)}
      </label>
      <div className={field('kind')}>
        <span className="field-label">What it does</span>
        <div className="seg" role="radiogroup" aria-label="What it does">
          <button type="button" role="radio" aria-checked={kind === 'command'} className={kind === 'command' ? 'on' : ''} disabled={staticSite} onClick={() => setKind('command')}>
            Run a command
          </button>
          <button type="button" role="radio" aria-checked={kind === 'path'} className={kind === 'path' ? 'on' : ''} onClick={() => setKind('path')}>
            Call a path
          </button>
        </div>
        {staticSite &&
          help('kind', `${project} is a static site, so there's no image to run a command in. A job can call a path on it instead.`)}
        {!staticSite && problem?.field === 'kind' && help('kind', '')}
      </div>
      {kind === 'command' ? (
        <label className={field('command')}>
          <span className="field-label">Command</span>
          <input className="input mono" placeholder="npm run cleanup" value={command} onChange={typed(setCommand)} spellCheck={false} autoCapitalize="off" autoComplete="off" />
          {help('command', "Runs with /bin/sh in a fresh container from production's image, with production's variables and the app's memory limit.")}
        </label>
      ) : (
        <div className={field('path')}>
          <span className="field-label">Path on production</span>
          <div className="method-path">
            <div className="seg" role="radiogroup" aria-label="Method">
              {(['GET', 'POST'] as const).map((m) => (
                <button key={m} type="button" role="radio" aria-checked={method === m} className={method === m ? 'on' : ''} onClick={() => setMethod(m)}>
                  {m}
                </button>
              ))}
            </div>
            <input className="input mono" placeholder="/api/cron" value={path} onChange={typed(setPath)} spellCheck={false} autoCapitalize="off" autoComplete="off" aria-label="Path" />
          </div>
          {help(
            'path',
            <>
              Pier sends <span className="mono">Authorization: Bearer</span> with the app's <span className="mono">{secretName}</span>, so the app can tell
              it's from Pier. Any 2xx answer counts as success.
            </>,
          )}
        </div>
      )}
      <div className="form-grid two">
        <label className={'field' + (problem?.field === 'schedule' || (bad && preview.field !== 'timezone') ? ' error' : '')}>
          <span className="field-label">Schedule</span>
          <input className="input mono" placeholder="0 3 * * *" value={schedule} onChange={typed(setSchedule)} spellCheck={false} autoCapitalize="off" autoComplete="off" />
          {problem?.field === 'schedule' ? (
            help('schedule', '')
          ) : !preview ? (
            <span className="t-note">Minute, hour, day of the month, month, day of the week.</span>
          ) : 'error' in preview ? (
            preview.field !== 'timezone' && (
              <span className="field-message" role="alert">
                {preview.error}
              </span>
            )
          ) : (
            <span className="t-note schedule-preview">
              <span className="t-strong">{preview.description}.</span>
              {preview.next[0] && <> Next {new Date(preview.next[0]).toLocaleString('en-GB', { weekday: 'short', day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' })}, your time.</>}
            </span>
          )}
        </label>
        <label className={field('timezone')}>
          <span className="field-label">Time zone</span>
          <input className="input mono" list="job-zones" value={timezone} onChange={typed(setTimezone)} spellCheck={false} autoCapitalize="off" autoComplete="off" />
          <datalist id="job-zones">
            {zones.map((z) => (
              <option key={z} value={z} />
            ))}
          </datalist>
          {help('timezone', 'The schedule is read in it. UTC unless you choose a place, like Europe/London.')}
        </label>
        <label className={field('timeout_minutes')}>
          <span className="field-label">Timeout</span>
          <select className="input" value={timeout} onChange={typed(setTimeout_)}>
            {[1, 5, 15, 30, 60].map((m) => (
              <option key={m} value={m}>
                {m === 1 ? '1 minute' : `${m} minutes`}
              </option>
            ))}
          </select>
          {help('timeout_minutes', 'A run still going then is stopped, and counts as failed.')}
        </label>
        <div className="field">
          <span className="field-label">Enabled</span>
          <label className="check">
            <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
            <span>Runs on its schedule</span>
          </label>
          <span className="t-note">Off, it runs only when you start it.</span>
        </div>
      </div>
    </Dialog>
  )
}
