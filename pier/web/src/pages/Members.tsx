import { api, type SessionRow } from '../lib/api'
import { useData } from '../lib/data'
import { ago } from '../lib/format'
import { Info, Terminal } from '../components/Icons'
import { confirm } from '../components/Dialog'
import { toast } from '../components/Toast'
import { PageHeading, Paging, Problem, Skeleton, usePaged, When } from '../components/ui'

type Data = { user: { name: string; email: string; created_at: string }; sessions: SessionRow[] }
const PAGE = 20

// Members is K3: the one owner, a plain note that invites come later, and
// every browser and computer signed in as them, each of which can be signed
// out.
export function Members({ now }: { now: number }) {
  const { data, error, reload } = useData<Data>('/api/sessions')
  const sessions = data?.sessions ?? []
  const paged = usePaged(sessions, PAGE, 0)

  const signOut = (s: SessionRow) => {
    const name = s.name || (s.kind === 'cli' ? 'pier CLI' : 'A browser')
    return confirm({
      title: `Sign out ${name}?`,
      body: (
        <p>
          {s.kind === 'cli'
            ? 'pier deploy stops working on that computer until someone runs pier login there again and you approve it.'
            : 'That browser goes back to the sign-in page the next time it loads anything.'}
        </p>
      ),
      action: 'Sign out',
      working: 'Signing out…',
      run: async () => {
        await api(`/api/sessions/${s.id}`, { method: 'DELETE' })
        reload()
      },
    }).then((ok) => ok && toast(`Signed out ${name}`))
  }
  const signOutOthers = () =>
    confirm({
      title: 'Sign out everywhere else?',
      body: (
        <p>
          Every other browser goes back to the sign-in page, and pier deploy stops working on every linked computer until it's
          approved again. This browser stays signed in.
        </p>
      ),
      action: 'Sign out everywhere else',
      working: 'Signing out…',
      run: async () => {
        await api('/api/sessions/others', { method: 'DELETE' })
        reload()
      },
    }).then((ok) => ok && toast('Signed out everywhere else'))

  return (
    <main className="main global">
      <PageHeading title="Members" note="Who can sign in to this server." />
      {error && <Problem>{error.message}</Problem>}
      {!data && !error && (
        <div className="panel">
          <Skeleton rows={3} tall />
        </div>
      )}
      {data && (
        <>
          <div className="stack">
            <div className="panel">
              <div className="activity-row sessions">
                <div className="event">
                  <div>
                    <span className="t-small t-strong">{data.user.name}</span>
                    <span className="t-note">{data.user.email}</span>
                  </div>
                </div>
                <span className="t-caption t-muted">Owner</span>
                <span className="t-note when">
                  Since{' '}
                  {new Date(data.user.created_at).toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric' })}
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
          </div>
          <div className="stack">
            <div className="toolbar">
              <div className="title">
                <h2 className="t-small t-strong">Where you're signed in</h2>
                <span className="count">{sessions.length}</span>
              </div>
              {sessions.length > 1 && (
                <button className="btn quiet" onClick={signOutOthers}>
                  Sign out everywhere else
                </button>
              )}
            </div>
            <section className="panel">
              {paged.shown.map((s) => {
                const active = now - new Date(s.last_seen_at).getTime() < 5 * 60_000
                return (
                  <div key={s.id} className="activity-row sessions">
                    <div className="event">
                      {s.kind === 'cli' ? <Terminal size={16} /> : <i className={'swatch ' + (s.current ? 'done' : '')} />}
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
                    {s.current || active ? (
                      <span className="t-note when">Active now</span>
                    ) : (
                      <When at={s.last_seen_at} now={now} className="t-note when" />
                    )}
                    {s.current ? (
                      <span />
                    ) : (
                      <button className="btn quiet" onClick={() => signOut(s)}>
                        Sign out
                      </button>
                    )}
                  </div>
                )
              })}
            </section>
            <Paging paged={paged} noun="session" />
          </div>
        </>
      )}
    </main>
  )
}
