import { useState } from 'react'
import { api, ApiError, type SessionRow } from '../lib/api'
import { useData } from '../lib/data'
import { ago } from '../lib/format'
import { Info } from '../components/Icons'

type Data = { user: { name: string; email: string; created_at: string }; sessions: SessionRow[] }

// Members is K3: the one owner, a plain note that invites come later, and
// every browser and computer signed in as them, each of which can be signed
// out.
export function Members({ now }: { now: number }) {
  const { data, error, reload } = useData<Data>('/api/sessions')
  const [failed, setFailed] = useState('')

  const signOut = async (path: string) => {
    setFailed('')
    try {
      await api(path, { method: 'DELETE' })
      reload()
    } catch (e) {
      setFailed(e instanceof ApiError ? e.message : "Can't reach pierd.")
    }
  }

  const sessions = data?.sessions ?? []
  return (
    <main className="main global">
      <div className="page-heading">
        <div>
          <h1 className="t-title" style={{ margin: 0 }}>
            Members
          </h1>
          <span className="t-caption t-muted">Who can sign in to this server.</span>
        </div>
      </div>
      {error && <span className="t-note">{error.message}</span>}
      {data && (
        <>
          <div className="panel">
            <div className="activity-row" style={{ borderBottom: 'none' }}>
              <div className="event">
                <div>
                  <span className="t-small">{data.user.name}</span>
                  <span className="t-note">{data.user.email}</span>
                </div>
              </div>
              <span className="t-caption t-muted">Owner</span>
              <span className="t-note when">
                Since{' '}
                {new Date(data.user.created_at).toLocaleDateString('en-GB', { day: 'numeric', month: 'short' })}
              </span>
              <span />
            </div>
          </div>
          <div className="alert info">
            <Info />
            <div>
              <span className="t-small t-strong">Inviting people comes in a later version</span>
              <span className="t-caption t-muted">
                Until then, this server has one account: the one <span className="mono">pierd init</span> made.
              </span>
            </div>
          </div>
          <section className="panel" style={{ marginTop: 12 }}>
            <div className="panel-head">
              <div style={{ flex: 1, display: 'flex', alignItems: 'baseline', gap: 10 }}>
                <span className="t-small t-strong">Where you're signed in</span>
                <span className="t-note">{sessions.length === 1 ? '1 session' : `${sessions.length} sessions`}</span>
              </div>
              {sessions.length > 1 && (
                <button className="btn quiet" onClick={() => signOut('/api/sessions/others')}>
                  Sign out everywhere else
                </button>
              )}
            </div>
            {sessions.map((s) => {
              const active = now - new Date(s.last_seen_at).getTime() < 5 * 60_000
              return (
                <div key={s.id} className="activity-row">
                  <div className="event">
                    <i className={'swatch ' + (s.current ? 'done' : '')} style={{ marginTop: 8 }} />
                    <div>
                      <span className="t-small">{s.name || (s.kind === 'cli' ? 'pier CLI' : 'A browser')}</span>
                      <span className="t-note">
                        {s.current
                          ? `This browser. Signed in ${ago(s.created_at, now).toLowerCase()} from ${s.ip}.`
                          : s.kind === 'cli'
                            ? `A token from pier login, approved ${ago(s.created_at, now).toLowerCase()}. Used by pier deploy.`
                            : `Signed in ${ago(s.created_at, now).toLowerCase()} from ${s.ip}.`}
                      </span>
                    </div>
                  </div>
                  <span className="t-caption t-muted">{s.kind === 'cli' ? 'CLI' : 'Dashboard'}</span>
                  <span className="t-note when">{s.current || active ? 'Active now' : ago(s.last_seen_at, now)}</span>
                  {s.current ? (
                    <span />
                  ) : (
                    <button className="btn quiet" onClick={() => signOut(`/api/sessions/${s.id}`)}>
                      Sign out
                    </button>
                  )}
                </div>
              )
            })}
          </section>
          {failed && <span className="field-message">{failed}</span>}
        </>
      )}
    </main>
  )
}
