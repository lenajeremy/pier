import { useEffect, useRef, useState } from 'react'
import { api, type Job, type JobRun } from '../lib/api'
import { useData } from '../lib/data'
import { seconds } from '../lib/format'
import { Link, navigate } from '../lib/router'
import { ChevronRight, Pencil, Trash } from '../components/Icons'
import { Menu, type MenuItem } from '../components/Menu'
import { Spinner, useBusy } from '../components/Spinner'
import { Paging, Problem, Skeleton, When } from '../components/ui'
import { JobDialog, jobsPath, remove, RunStatus, runNow, toggle, Until, what } from './Jobs'
import { toast } from '../components/Toast'
import { Gate, useRole, viewerCant } from '../lib/role'

const PER = 25

// result is how a run ended, in a few words: its exit code or HTTP status.
function result(r: JobRun): string {
  if (r.status === 'skipped') return 'Skipped'
  if (r.status === 'running') return 'Running'
  if (r.http_status != null) return `HTTP ${r.http_status}`
  if (r.exit_code != null) return `Exit code ${r.exit_code}`
  return 'No result'
}

// explains says whether a run's reason adds to its result: "It exited with
// code 3" doesn't, beside Exit code 3; a timeout or a missing image does.
const explains = (r: JobRun) =>
  !!r.detail &&
  r.status !== 'succeeded' &&
  !(r.exit_code != null && r.detail === `It exited with code ${r.exit_code}.`) &&
  !(r.http_status != null && / answered \d{3}\b/.test(r.detail))

const took = (r: JobRun, now: number) =>
  r.status === 'skipped' ? '' : seconds((r.finished_at ? new Date(r.finished_at).getTime() : now) - new Date(r.started_at).getTime())

function Crumbs({ project, job, run }: { project: string; job: Pick<Job, 'id' | 'name'>; run?: number }) {
  return (
    <nav className="breadcrumb" aria-label="Breadcrumb">
      <Link to={`/projects/${project}/jobs`}>Jobs</Link>
      <ChevronRight />
      {run ? <Link to={`/projects/${project}/jobs/${job.id}`}>{job.name}</Link> : <span style={{ color: 'var(--d-text)' }}>{job.name}</span>}
      {run && (
        <>
          <ChevronRight />
          <span className="mono" style={{ color: 'var(--d-text)' }}>
            Run {run}
          </span>
        </>
      )}
    </nav>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="detail">
      <span className="t-note">{label}</span>
      <div className="t-caption" style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
        {children}
      </div>
    </div>
  )
}

// JobPage is one job: what it does and when, Run now, and every run it
// has had, newest first, a page at a time.
export function JobPage({ project, id, now }: { project: string; id: string; now: number }) {
  const base = `${jobsPath(project)}/${encodeURIComponent(id)}`
  const job = useData<{ job: Job }>(base, () => true, (d) => (d.job.running ? 2000 : 15_000))
  const [page, setPage] = useState(0)
  const runs = useData<{ runs: JobRun[]; total: number; page: number; per: number }>(
    `${base}/runs?page=${page}&per=${PER}`,
    () => true,
    (d) => (d.runs.some((r) => r.status === 'running') || job.data?.job.running ? 2000 : 15_000),
  )
  const [editing, setEditing] = useState(false)
  const [busy, run] = useBusy<'run'>()
  const { change } = useRole()
  const reload = () => {
    job.reload()
    runs.reload()
  }
  const meta = useData<{ production: { server: boolean } | null; secret_name: string }>(editing ? jobsPath(project) : null)

  if (job.error) {
    return (
      <main className="main">
        <Problem>{job.error.status === 404 ? `${project} has no job ${id}.` : job.error.message}</Problem>
      </main>
    )
  }
  if (!job.data) {
    return (
      <main className="main">
        <div className="panel">
          <Skeleton rows={4} tall />
        </div>
      </main>
    )
  }
  const j = job.data.job
  const items: MenuItem[] = [
    { label: 'Edit', icon: <Pencil />, onSelect: () => setEditing(true) },
    { label: j.enabled ? 'Turn off its schedule' : 'Turn on its schedule', onSelect: () => toggle(project, j, reload) },
    'separator',
    { label: 'Delete', icon: <Trash />, danger: true, disabled: j.running, onSelect: () => remove(project, j, () => navigate(`/projects/${project}/jobs`)) },
  ]
  const list = runs.data
  const paged = { at: page, pages: list ? Math.max(1, Math.ceil(list.total / PER)) : 1, per: PER, total: list?.total ?? 0, setPage }

  return (
    <main className="main" style={{ gap: 24 }}>
      <Crumbs project={project} job={j} />
      <section className="panel">
        <div className="card-head">
          <h2 className="t-small t-strong">{j.name}</h2>
          <div className="card-actions">
            <Gate allow={change} reason={viewerCant('run jobs')}>
              <button className="btn primary" disabled={j.running || busy === 'run'} onClick={() => run('run', () => runNow(project, j, reload))}>
                {(j.running || busy === 'run') && <Spinner />}
                {j.running ? 'Running…' : 'Run now'}
              </button>
            </Gate>
            {change && <Menu label={`More for ${j.name}`} items={items} />}
          </div>
        </div>
        <div className="details job-details">
          <div className="details-row">
            <Field label={j.kind === 'command' ? 'Runs' : 'Calls'}>
              <span className="mono">{what(j)}</span>
            </Field>
            <Field label="Schedule">
              <span>{j.description}</span>
              <span className="mono t-muted">
                {j.schedule}
                {j.timezone !== 'UTC' ? `, ${j.timezone}` : ''}
              </span>
            </Field>
            <Field label="Next run">
              {!j.enabled ? (
                <span className="t-muted">Its schedule is off. It runs when you start it.</span>
              ) : j.next_run ? (
                <Until at={j.next_run} now={now} />
              ) : (
                <span className="t-muted">Never: no date matches the schedule.</span>
              )}
            </Field>
            <Field label="Timeout">{Math.round(j.timeout_s / 60) === 1 ? '1 minute' : `${Math.round(j.timeout_s / 60)} minutes`}</Field>
          </div>
        </div>
      </section>

      <div className="stack">
        <div className="toolbar">
          <div className="title">
            <h2 className="t-small t-strong">Runs</h2>
            {list && <span className="count">{list.total.toLocaleString('en-GB')}</span>}
          </div>
        </div>
        {runs.error ? (
          <Problem>{runs.error.message}</Problem>
        ) : !list ? (
          <div className="panel">
            <Skeleton rows={5} />
          </div>
        ) : list.total === 0 ? (
          <div className="empty">
            <span className="t-small t-strong">No runs yet</span>
            <span className="t-caption t-muted">
              {j.enabled && j.next_run ? (
                <>
                  The first is <Until at={j.next_run} now={now} />, or start one now.
                </>
              ) : (
                'Start one now to see its output here.'
              )}
            </span>
          </div>
        ) : (
          <div className="table runs">
            <div className="t-head">
              <span>Run</span>
              <span>Status</span>
              <span>Result</span>
              <span>Started</span>
              <span>Took</span>
            </div>
            {list.runs.map((r) => {
              const to = `/projects/${project}/jobs/${j.id}/runs/${r.id}`
              return (
                <div key={r.id} className="t-row clickable" onClick={() => navigate(to)}>
                  <Link to={to} className="mono" onClick={(e) => e.stopPropagation()}>
                    {r.id}
                  </Link>
                  <RunStatus run={r} />
                  <span className={'t-muted' + (r.status === 'failed' ? ' run-why' : '')} data-tip={r.detail || undefined}>
                    {explains(r) ? r.detail : result(r)}
                  </span>
                  <span className="t-muted">
                    <When at={r.started_at} now={now} />
                    {r.started_by === 'manual' ? (r.run_by ? `, run by ${r.run_by}` : ', by hand') : ''}
                  </span>
                  <span className="mono t-muted">{took(r, now)}</span>
                </div>
              )
            })}
          </div>
        )}
        <Paging paged={paged} noun="run" />
      </div>

      {editing && meta.data && (
        <JobDialog
          project={project}
          job={j}
          staticSite={!!meta.data.production && !meta.data.production.server}
          secretName={meta.data.secret_name}
          onClose={() => setEditing(false)}
          onDone={(saved) => {
            setEditing(false)
            reload()
            toast(`Saved ${saved.name}`)
          }}
        />
      )}
    </main>
  )
}

type Line = { ms: number; stream: string; text: string }

// JobRunPage is one run: how it ended, and its output, followed while it
// runs.
export function JobRunPage({ project, id, run: runID, now }: { project: string; id: string; run: string; now: number }) {
  const base = `${jobsPath(project)}/${encodeURIComponent(id)}`
  const job = useData<{ job: Job }>(base)
  const [r, setRun] = useState<JobRun | null>(null)
  const [lines, setLines] = useState<Line[]>([])
  const [problem, setProblem] = useState('')
  const next = useRef(0)
  const box = useRef<HTMLDivElement>(null)

  useEffect(() => {
    setLines([])
    setRun(null)
    next.current = 0
    let stopped = false
    let timer: number | undefined
    const pull = async () => {
      try {
        const d = await api<{ run: JobRun; lines: Line[]; next: number; done: boolean }>(`${base}/runs/${encodeURIComponent(runID)}?from=${next.current}`)
        if (stopped) return
        setRun(d.run)
        setProblem('')
        if (d.lines.length) {
          next.current = d.next
          setLines((ls) => [...ls, ...d.lines])
        }
        if (!d.done) timer = window.setTimeout(pull, 1000)
      } catch (e) {
        if (stopped) return
        setProblem(e instanceof Error ? e.message : "Can't reach pierd.")
        timer = window.setTimeout(pull, 3000)
      }
    }
    pull()
    return () => {
      stopped = true
      window.clearTimeout(timer)
    }
  }, [base, runID])

  useEffect(() => {
    const el = box.current
    if (el && r?.status === 'running') el.scrollTop = el.scrollHeight
  }, [lines.length, r?.status])

  if (!r || !job.data) {
    return (
      <main className="main">
        {problem ? (
          <Problem>{problem}</Problem>
        ) : (
          <div className="panel">
            <Skeleton rows={4} tall />
          </div>
        )}
      </main>
    )
  }
  const j = job.data.job
  return (
    <main className="main" style={{ gap: 24 }}>
      <Crumbs project={project} job={j} run={r.id} />
      <section className="panel">
        <div className="card-head">
          <h2 className="t-small t-strong">Run details</h2>
        </div>
        <div className="details job-details">
          <div className="details-row">
            <Field label="Status">
              <RunStatus run={r} />
            </Field>
            <Field label="Result">{result(r)}</Field>
            <Field label="Started">
              <span>
                <When at={r.started_at} now={now} />
                {r.started_by === 'manual' ? (r.run_by ? `, run by ${r.run_by}` : ', by hand') : r.scheduled_for ? ', on its schedule' : ''}
              </span>
            </Field>
            <Field label="Took">{r.status === 'skipped' ? 'It didn’t run' : took(r, now) + (r.status === 'running' ? ' so far' : '')}</Field>
          </div>
          {explains(r) && (
            <Field label={r.status === 'skipped' ? 'Why it was skipped' : 'Why it failed'}>
              <span className={r.status === 'failed' ? 'run-why' : ''}>{r.detail}</span>
            </Field>
          )}
          {r.deployment && (
            <Field label="Against production's deployment">
              <Link className="mono" to={`/projects/${project}/deployments/${r.deployment}`}>
                {r.deployment}
              </Link>
            </Field>
          )}
        </div>
      </section>

      <section className="sections">
        <div className="section-row" style={{ cursor: 'default' }}>
          <span style={{ width: 14 }} />
          <span className="title">Output</span>
          <span className="t-note">{lines.length ? `${lines.length.toLocaleString('en-GB')} lines` : ''}</span>
        </div>
        <div className="section-body">
          {lines.length === 0 ? (
            <div className="log-empty">{r.status === 'running' ? 'Waiting for the first line…' : r.status === 'skipped' ? 'A skipped run has no output.' : 'This run printed nothing.'}</div>
          ) : (
            <div className="log" ref={box}>
              {lines.map((l, i) => (
                <div key={i} className={'line' + (l.stream === 'stderr' ? ' error' : '')}>
                  <span className="time">{(l.ms / 1000).toFixed(2)}</span>
                  <span className={'tag ' + (l.stream === 'pier' ? 'pier' : l.stream === 'stderr' ? 'build' : 'deploy')}>{l.stream}</span>
                  <span className={'text' + (l.text.startsWith('$ ') ? ' cmd' : '')}>{l.text}</span>
                </div>
              ))}
            </div>
          )}
        </div>
      </section>
    </main>
  )
}
