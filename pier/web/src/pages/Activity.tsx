import { useState } from 'react'
import { type ProjectSummary } from '../lib/api'
import { useData } from '../lib/data'
import { ago } from '../lib/format'
import { Link } from '../lib/router'
type Event = { id: number; at: number; project: string; title: string; detail: string; deployment?: string }
export function Activity({ projects, now }: { projects: ProjectSummary[]; now: number }) {
  const [project, filter] = useState(''),
    [pages, setPages] = useState<number[]>([])
  const before = pages.at(-1) ?? 0
  const { data, error } = useData<{ events: Event[] }>(
    '/api/activity?project=' + encodeURIComponent(project) + '&before=' + before,
    () => before === 0,
    5000,
  )
  const events = data?.events ?? []
  const groups = new Map<string, Event[]>()
  for (const e of events) {
    const day = new Date(e.at).toLocaleDateString(undefined, { dateStyle: 'long' })
    if (!groups.has(day)) groups.set(day, [])
    groups.get(day)!.push(e)
  }
  const today = new Date(now).toLocaleDateString(undefined, { dateStyle: 'long' })
  return (
    <main className="main global operations">
      <header className="page-heading">
        <div>
          <h1 className="t-title">Activity</h1>
          <p className="t-caption t-muted">What happened on this server, newest first.</p>
        </div>
        <select
          className="input ops-select"
          aria-label="Filter activity by project"
          value={project}
          onChange={(e) => {
            filter(e.target.value)
            setPages([])
          }}
        >
          <option value="">All projects</option>
          {projects.map((p) => (
            <option key={p.name}>{p.name}</option>
          ))}
        </select>
      </header>
      {error && <p role="alert">{error.message}</p>}
      {data && (
        <section className="panel ops-feed">
          {!events.length && <p className="ops-empty">No activity yet.</p>}
          {[...groups].map(([day, events]) => (
            <div key={day}>
              <h2 className="ops-date">{day === today ? 'Today' : day}</h2>
              {events.map((e) => (
                <div className="ops-row" key={e.id}>
                  <i
                    className={'swatch ' + (e.title.includes('failed') ? 'failed' : e.deployment ? 'ready' : 'asleep')}
                  />
                  <div>
                    <strong>{e.title}</strong>
                    {e.deployment && (
                      <>
                        {' '}
                        <Link className="mono" to={'/projects/' + e.project + '/deployments/' + e.deployment}>
                          {e.deployment}
                        </Link>
                      </>
                    )}
                    {e.detail && <small>{e.detail}</small>}
                  </div>
                  {e.project ? (
                    <Link to={'/projects/' + e.project}>{e.project}</Link>
                  ) : (
                    <span className="t-note">Server</span>
                  )}
                  <time className="t-note" title={new Date(e.at).toLocaleString()}>
                    {ago(new Date(e.at).toISOString(), now)}
                  </time>
                </div>
              ))}
            </div>
          ))}
        </section>
      )}
      <div className="ops-actions">
        {before > 0 && (
          <button className="btn quiet" onClick={() => setPages(pages.slice(0, -1))}>
            Newer events
          </button>
        )}
        {events.length === 100 && (
          <button className="btn quiet" onClick={() => setPages([...pages, events.at(-1)!.id])}>
            Older events
          </button>
        )}
      </div>
    </main>
  )
}
