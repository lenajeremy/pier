import { useEffect, useRef, useState } from 'react'
import type { Address, Deployment, LogLine } from '../lib/api'
import { api, ApiError } from '../lib/api'
import { useData } from '../lib/data'
import { ago, exact, plural, seconds, short, size } from '../lib/format'
import { Link, navigate } from '../lib/router'
import * as I from '../components/Icons'
import { deploymentStatus } from '../components/ui'
import { StatusFlaps } from '../flap/Flaps'

type Detail = { deployment: Deployment; addresses: Address[] }

// DeploymentPage is one deployment (D3, D4, E3): what it is, where it's
// served, and its build log, which follows the build while it runs.
export function DeploymentPage({ project, id, now }: { project: string; id: string; now: number }) {
  const [moving, setMoving] = useState('')
  const { data, error, reload } = useData<Detail>(
    `/api/projects/${project}/deployments/${id}`,
    (d) => d.deployment.status === 'building',
    1500,
  )
  if (error) return <main className="main"><span className="t-note">{error.message}</span></main>
  if (!data) return <div className="loading">Loading {id}…</div>
  const d = data.deployment
  const st = deploymentStatus(d)
  const building = d.status === 'building'
  const env = d.target === 'production' ? 'Production' : 'Preview'
  const live = data.addresses.find((a) => a.kind !== 'deployment')

  return (
    <main className="main" style={{ gap: 24 }}>
      <nav className="breadcrumb" aria-label="Breadcrumb">
        <Link to={`/projects/${project}`}>{project}</Link>
        <I.ChevronRight />
        <Link to={`/projects/${project}`}>Deployments</Link>
        <I.ChevronRight />
        <span className="mono" style={{ color: 'var(--d-text)' }}>
          {d.id}
        </span>
      </nav>

      <section className="panel">
        <div className="card-head">
          <span className="t-small t-strong">Deployment details</span>
          <div className="card-actions">
            {moving && <span className="t-note">{moving}</span>}
            {d.status !== 'building' && (
              <button
                className="btn quiet icon"
                onClick={async () => {
                  setMoving('')
                  try {
                    const r = await api<{ id?: string }>(`/api/projects/${encodeURIComponent(project)}/redeploy`, {
                      method: 'POST',
                      body: JSON.stringify({ id: d.id }),
                    })
                    if (r.id) navigate(`/projects/${project}/deployments/${r.id}`)
                  } catch (e) {
                    setMoving(e instanceof ApiError ? e.message : "Can't reach pierd.")
                  }
                }}
              >
                <I.Redeploy />
                Redeploy
              </button>
            )}
            {d.status === 'ready' && d.role !== 'production' && (
              <button
                className="btn quiet"
                onClick={async () => {
                  setMoving('')
                  try {
                    await api(`/api/projects/${encodeURIComponent(project)}/promote`, {
                      method: 'POST',
                      body: JSON.stringify({ id: d.id }),
                    })
                    reload()
                  } catch (e) {
                    setMoving(e instanceof ApiError ? e.message : "Can't reach pierd.")
                  }
                }}
              >
                Promote to production
              </button>
            )}
            {d.status === 'ready' && (
              <a className="btn quiet visit" href={(live ?? data.addresses[0])?.url} target="_blank" rel="noreferrer">
                Visit
                <I.Visit />
              </a>
            )}
          </div>
        </div>
        <div className="card-body">
          <Preview d={d} url={data.addresses.find((a) => a.kind === 'deployment')?.url} />
          <div className="details">
            <div className="details-row">
              <Field label="Created">
                <span title={exact(d.created_at)}>{ago(d.created_at, now)}</span>
              </Field>
              <Field label="Status">
                <StatusFlaps status={st.swatch} word={st.word} />
              </Field>
              <Field label="Duration">
                {building ? `${seconds(now - new Date(d.created_at).getTime())} so far` : seconds(d.build_ms)}
              </Field>
              <Field label="Environment">{env}</Field>
            </div>
            <Field label="Domains">
              {data.addresses.length === 0 ? (
                <span className="t-muted">{building ? 'Given once the build works.' : 'None. A failed build gets no address.'}</span>
              ) : (
                data.addresses.map((a, i) => (
                  <a
                    key={a.host}
                    className="mono"
                    href={a.url}
                    target="_blank"
                    rel="noreferrer"
                    style={{ color: i === 0 ? 'var(--d-text)' : 'var(--d-muted)', alignSelf: 'flex-start' }}
                  >
                    {a.host}
                  </a>
                ))
              )}
            </Field>
            <Field label="Source">
              {d.source === 'cli' && <span className="t-muted">Uploaded with pier deploy</span>}
              {d.branch && (
                <span className="line">
                  <I.Branch />
                  <span className="mono">{d.branch}</span>
                </span>
              )}
              {(d.commit || d.message) && (
                <span className="line">
                  <I.Commit />
                  {d.commit && <span className="mono t-muted">{short(d.commit)}</span>}
                  <span>{d.message}</span>
                </span>
              )}
            </Field>
          </div>
        </div>
      </section>

      <Sections project={project} d={d} />
    </main>
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

function Preview({ d, url }: { d: Deployment; url?: string }) {
  if (d.status === 'failed') {
    return (
      <div className="preview-box failed">
        <span className="t-caption t-strong" style={{ color: 'var(--d-red-text)' }}>
          Build failed
        </span>
        <span className="t-caption t-muted" style={{ marginTop: 4 }}>
          {d.error}
        </span>
      </div>
    )
  }
  if (d.status === 'building' || !url) {
    return (
      <div className="preview-box">
        <span className="t-note">The preview appears once it's live</span>
      </div>
    )
  }
  return (
    <a className="preview-box" href={url} target="_blank" rel="noreferrer" aria-label={`Open ${d.id}`}>
      <iframe src={url} title={`Preview of ${d.id}`} tabIndex={-1} loading="lazy" sandbox="allow-scripts" />
    </a>
  )
}

function Sections({ project, d }: { project: string; d: Deployment }) {
  const building = d.status === 'building'
  const [open, setOpen] = useState(d.status !== 'ready')
  const [lines, setLines] = useState<LogLine[]>([])
  const next = useRef(0)

  useEffect(() => {
    setLines([])
    next.current = 0
  }, [d.id])

  useEffect(() => {
    let stopped = false
    let timer: number | undefined
    const pull = async () => {
      try {
        const r = await api<{ lines: LogLine[]; next: number; done: boolean }>(
          `/api/projects/${project}/deployments/${d.id}/log?from=${next.current}`,
        )
        if (stopped) return
        if (r.lines.length) {
          next.current = r.next
          setLines((ls) => [...ls, ...r.lines])
        }
        if (!r.done) timer = window.setTimeout(pull, 1000)
      } catch {
        if (!stopped) timer = window.setTimeout(pull, 3000)
      }
    }
    pull()
    return () => {
      stopped = true
      window.clearTimeout(timer)
    }
  }, [project, d.id, building])

  useEffect(() => {
    if (d.status === 'failed') setOpen(true)
  }, [d.status])

  const errors = lines.filter(isError).length
  const meta = lines.length === 0 ? '' : plural(lines.length, 'line') + (errors ? `, ${plural(errors, 'error')}` : '')

  return (
    <section className="sections">
      <button className={'section-row' + (open ? ' open' : '')} onClick={() => setOpen(!open)} aria-expanded={open}>
        <I.Disclosure />
        <span className="title">Build log</span>
        <span className="t-note">{meta}</span>
        <span className="mono t-caption" style={{ color: 'var(--d-note)' }}>
          {building ? '' : seconds(d.build_ms)}
        </span>
        {building ? <I.Working /> : d.status === 'failed' ? <I.FailedMark /> : <I.Done />}
      </button>
      {open && (
        <div className="section-body">
          <Log lines={lines} following={building} />
        </div>
      )}
      {d.runtime && d.status === 'ready' && <RuntimeLogSection project={project} id={d.id} />}
      <div className="section-row" style={{ cursor: 'default', opacity: d.status === 'ready' ? 1 : 0.6 }}>
        <span style={{ width: 14 }} />
        <span className="title">Output</span>
        <span className="t-note">
          {d.status === 'ready'
            ? d.runtime ? `HTTP server · ${d.framework}` : `${plural(d.files, 'file')}, ${size(d.bytes)}${d.framework ? `, built with ${d.framework}` : ''}`
            : d.status === 'building'
              ? 'Waiting for the build'
              : 'Skipped'}
        </span>
      </div>
    </section>
  )
}

function isError(l: LogLine) {
  return (l.tag === 'build' || l.tag === 'pier') && /\b(error|ERR!|failed|fatal)\b/i.test(l.text) && !/\b0 errors?\b/i.test(l.text)
}

function Log({ lines, following }: { lines: LogLine[]; following: boolean }) {
  const box = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const el = box.current
    if (el && following) el.scrollTop = el.scrollHeight
  }, [lines.length, following])
  if (lines.length === 0) {
    return <div className="log-empty">{following ? 'Waiting for the first line…' : 'This build kept no log.'}</div>
  }
  return (
    <div className="log" ref={box}>
      {lines.map((l, i) => (
        <div key={i} className={'line' + (isError(l) ? ' error' : '')}>
          <span className="time">{(l.ms / 1000).toFixed(2)}</span>
          <span className={'tag ' + l.tag}>{l.tag}</span>
          <span className={'text' + (l.text.startsWith('$ ') ? ' cmd' : '')}>{l.text}</span>
        </div>
      ))}
    </div>
  )
}

type RuntimeLine = { at?: string; stream: string; text: string }

function RuntimeLogSection({ project, id }: { project: string; id: string }) {
  const [open, setOpen] = useState(false)
  const [lines, setLines] = useState<RuntimeLine[]>([])
  const [err, setErr] = useState('')
  const box = useRef<HTMLDivElement>(null)

  useEffect(() => {
    setLines([])
    setErr('')
  }, [id])

  useEffect(() => {
    if (!open) return
    let stopped = false
    let timer: number | undefined
    const pull = async () => {
      try {
        const r = await api<{ lines: RuntimeLine[] }>(
          `/api/projects/${encodeURIComponent(project)}/deployments/${encodeURIComponent(id)}/runtime-log?tail=200`,
        )
        if (stopped) return
        setLines(r.lines)
        setErr('')
      } catch (e) {
        if (!stopped) setErr(e instanceof ApiError ? e.message : "Can't reach pierd.")
      }
      if (!stopped) timer = window.setTimeout(pull, 2000)
    }
    pull()
    return () => {
      stopped = true
      window.clearTimeout(timer)
    }
  }, [project, id, open])

  useEffect(() => {
    if (open && box.current) box.current.scrollTop = box.current.scrollHeight
  }, [lines.length, open])

  return (
    <>
      <button className={'section-row' + (open ? ' open' : '')} onClick={() => setOpen(!open)} aria-expanded={open}>
        <I.Disclosure />
        <span className="title">Runtime logs</span>
        <span className="t-note">{lines.length ? plural(lines.length, 'line') : 'stdout and stderr'}</span>
      </button>
      {open && (
        <div className="section-body">
          {err ? (
            <div className="log-empty">{err}</div>
          ) : lines.length === 0 ? (
            <div className="log-empty">No container output yet.</div>
          ) : (
            <div className="log" ref={box}>
              {lines.map((l, i) => (
                <div key={i} className={'line' + (l.stream === 'stderr' ? ' error' : '')}>
                  <span className="time">{l.at ? l.at.slice(11, 19) : ''}</span>
                  <span className={'tag ' + (l.stream === 'stderr' ? 'build' : 'pier')}>{l.stream}</span>
                  <span className="text">{l.text}</span>
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </>
  )
}
