import { useState } from 'react'
import { type ProjectSummary } from '../lib/api'
import { useData } from '../lib/data'
import { exact } from '../lib/format'
import { Link } from '../lib/router'
import { ChevronLeft, ChevronRight } from '../components/Icons'
import { PageHeading, Problem, Skeleton, When } from '../components/ui'

type Event = { id: number; at: number; project: string; title: string; detail: string; deployment?: string; actor?: string }
const PAGE = 100 // what pierd sends at a time

// The swatch says what kind of thing happened, in the colours the rest of
// the dashboard uses for it.
function swatch(e: Event): string {
  if (/failed/i.test(e.title)) return 'failed'
  if (/^(Promoted|Rolled back|Undid)/.test(e.title)) return 'production'
  if (/^Build ready/.test(e.title)) return 'ready'
  if (/^(Disk|Memory)/.test(e.title)) return 'warning'
  return ''
}

const dayOf = (at: number) => {
  const d = new Date(at)
  return new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime()
}

function dayName(day: number, now: number): string {
  const today = dayOf(now)
  if (day === today) return 'Today'
  if (day === dayOf(today - 1)) return 'Yesterday'
  const d = new Date(day)
  return d.toLocaleDateString('en-GB', {
    weekday: 'long',
    day: 'numeric',
    month: 'long',
    ...(d.getFullYear() === new Date(now).getFullYear() ? {} : { year: 'numeric' }),
  })
}

// Activity is K1: what happened on this server, newest first, a day at a time.
export function Activity({ projects, now }: { projects: ProjectSummary[]; now: number }) {
  const [project, setProject] = useState('')
  // Each page starts before the oldest event of the page above it.
  const [pages, setPages] = useState<number[]>([])
  const before = pages.at(-1) ?? 0
  const { data, error } = useData<{ events: Event[] }>(
    '/api/activity?project=' + encodeURIComponent(project) + '&before=' + before,
    () => before === 0,
    5000,
  )
  const events = data?.events ?? []
  const groups: { day: number; events: Event[] }[] = []
  for (const e of events) {
    const day = dayOf(e.at)
    if (groups.at(-1)?.day !== day) groups.push({ day, events: [] })
    groups.at(-1)!.events.push(e)
  }
  const more = events.length === PAGE

  return (
    <main className="main global">
      <PageHeading title="Activity" note="What happened on this server, newest first.">
        {projects.length > 1 && (
          <select
            className="input compact"
            aria-label="Show activity for"
            value={project}
            onChange={(e) => {
              setProject(e.target.value)
              setPages([])
            }}
          >
            <option value="">All projects</option>
            {projects.map((p) => (
              <option key={p.name}>{p.name}</option>
            ))}
          </select>
        )}
      </PageHeading>
      {error && <Problem>{error.message}</Problem>}
      {!data && !error && (
        <div className="panel">
          <Skeleton rows={6} tall />
        </div>
      )}
      {data && events.length === 0 && (
        <div className="empty">
          <span className="t-small t-strong">{project ? `Nothing has happened in ${project} yet` : 'Nothing has happened yet'}</span>
          <span className="t-caption t-muted" style={{ maxWidth: 480 }}>
            Deployments, rollbacks, sign-ins and changes to settings are listed here as they happen.
          </span>
        </div>
      )}
      {groups.length > 0 && (
        <section className="panel feed">
          {groups.map((g) => (
            <div key={g.day} className="feed-day">
              <h2 className="feed-date">{dayName(g.day, now)}</h2>
              {g.events.map((e) => (
                <div className="activity-row" key={e.id}>
                  <div className="event">
                    <i className={'swatch ' + swatch(e)} />
                    <div>
                      <span className="t-small">
                        <span className="t-strong">{e.title}</span>
                        {e.deployment && (
                          <>
                            {' '}
                            <Link className="mono id" to={'/projects/' + e.project + '/deployments/' + e.deployment}>
                              {e.deployment}
                            </Link>
                          </>
                        )}
                      </span>
                      {(e.detail || e.actor) && (
                        <span className="t-note">
                          {e.detail}
                          {e.actor && (e.detail ? `, by ${e.actor}` : `By ${e.actor}`)}
                        </span>
                      )}
                    </div>
                  </div>
                  {e.project ? (
                    <Link className="t-caption where" to={'/projects/' + e.project}>
                      {e.project}
                    </Link>
                  ) : (
                    <span className="t-caption t-quiet">Server</span>
                  )}
                  {g.day === dayOf(now) ? (
                    <When at={e.at} now={now} className="t-note when" />
                  ) : (
                    <time className="t-note when" data-tip={exact(new Date(e.at).toISOString())} data-tip-mono>
                      {new Date(e.at).toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' })}
                    </time>
                  )}
                </div>
              ))}
            </div>
          ))}
        </section>
      )}
      {(before > 0 || more) && (
        <div className="paging">
          <span className="t-note">
            {before > 0 ? `Page ${pages.length + 1}. ` : ''}
            {events.length > 0 &&
              `From ${new Date(events.at(-1)!.at).toLocaleDateString('en-GB', { day: 'numeric', month: 'short' })} to ${new Date(events[0].at).toLocaleDateString('en-GB', { day: 'numeric', month: 'short' })}.`}
          </span>
          <div className="paging-buttons">
            <button className="btn quiet icon" disabled={before === 0} onClick={() => setPages(pages.slice(0, -1))}>
              <ChevronLeft color={before === 0 ? '#5E667B' : '#ECEEF4'} />
              Newer
            </button>
            <button className="btn quiet visit" disabled={!more} onClick={() => setPages([...pages, events.at(-1)!.id])}>
              Older
              <ChevronRight size={16} color={more ? '#ECEEF4' : '#5E667B'} />
            </button>
          </div>
        </div>
      )}
    </main>
  )
}
