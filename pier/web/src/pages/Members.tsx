import { useMemo, useState, type FormEvent } from 'react'
import { api, type Invite, type Member, type SessionRow } from '../lib/api'
import { useData } from '../lib/data'
import { ago } from '../lib/format'
import { Gate, onlyOwner, roleName, useRole } from '../lib/role'
import { Trash } from '../components/Icons'
import { confirm, Dialog } from '../components/Dialog'
import { Menu, type MenuItem } from '../components/Menu'
import { Spinner } from '../components/Spinner'
import { copy, toast } from '../components/Toast'
import { PageHeading, Paging, Problem, SearchField, Skeleton, usePaged, When } from '../components/ui'

type Sessions = { sessions: SessionRow[] }
const PAGE = 20

// Recently enough to say "Active now".
const activeNow = (at: string, now: number) => now - new Date(at).getTime() < 5 * 60_000

// What each role can do, said the same in the invite and in a role change.
const roleDoes = {
  member: 'Deploys, promotes, rolls back and changes every project.',
  viewer: 'Looks at everything. Sees variables by name, never their values.',
}

const who = (m: Member) => m.name || m.email

// Members is K3: everyone who can sign in to this server and what each may
// do, the invites still waiting, and every browser and computer signed in
// as you. The owner invites, changes roles and removes people.
export function Members({ now }: { now: number }) {
  const { owner } = useRole()
  const people = useData<{ members: Member[] }>('/api/members')
  const invites = useData<{ invites: Invite[] }>(owner ? '/api/invites' : null)
  const [inviting, setInviting] = useState(false)
  const [query, setQuery] = useState('')

  const all = people.data?.members ?? []
  const found = useMemo(() => {
    const q = query.trim().toLowerCase()
    return all.filter((m) => !q || m.name.toLowerCase().includes(q) || m.email.toLowerCase().includes(q) || m.role.includes(q))
  }, [all, query])
  const paged = usePaged(found, PAGE, query)
  const waiting = invites.data?.invites ?? []

  const setRole = (m: Member, role: 'member' | 'viewer') =>
    confirm({
      title: `Make ${who(m)} ${role === 'member' ? 'a member' : 'a viewer'}?`,
      body: (
        <p>
          {role === 'member'
            ? `${who(m)} can deploy, promote, roll back and change every project, from their next click, in every browser and on every computer they use.`
            : `${who(m)} can still look at everything, but can't deploy or change anything, and sees variables by name only. It applies from their next click, in every browser and on every computer they use.`}
        </p>
      ),
      action: role === 'member' ? 'Make a member' : 'Make a viewer',
      working: 'Changing…',
      run: async () => {
        await api(`/api/members/${m.id}`, { method: 'PUT', body: JSON.stringify({ role }) })
        people.reload()
      },
    }).then((ok) => ok && toast(`${who(m)} is now ${role === 'member' ? 'a member' : 'a viewer'}`))

  const remove = (m: Member) =>
    confirm({
      title: `Remove ${who(m)} from this server?`,
      body: (
        <p>
          {who(m)} is signed out of every browser, and pier stops working on every computer they approved. To come
          back, they need a new invite. What they deployed stays as it is.
        </p>
      ),
      action: 'Remove',
      working: 'Removing…',
      danger: true,
      run: async () => {
        await api(`/api/members/${m.id}`, { method: 'DELETE' })
        people.reload()
      },
    }).then((ok) => ok && toast(`Removed ${who(m)}`))

  const revoke = (inv: Invite) =>
    confirm({
      title: 'Revoke this invite?',
      body: <p>The link stops working at once. Nobody has used it yet, so nobody loses access.</p>,
      action: 'Revoke',
      working: 'Revoking…',
      danger: true,
      run: async () => {
        await api(`/api/invites/${inv.id}`, { method: 'DELETE' })
        invites.reload()
      },
    }).then((ok) => ok && toast('Revoked the invite'))

  const actions = (m: Member): MenuItem[] => [
    m.role === 'viewer'
      ? { label: `Make ${who(m)} a member`, onSelect: () => setRole(m, 'member') }
      : { label: `Make ${who(m)} a viewer`, onSelect: () => setRole(m, 'viewer') },
    'separator',
    { label: `Remove ${who(m)}`, icon: <Trash />, danger: true, onSelect: () => remove(m) },
  ]

  return (
    <main className="main global">
      <PageHeading
        title="Members"
        note={owner ? 'Who can sign in to this server, and what each person can do.' : 'Who can sign in to this server. Only the owner invites people and changes what they can do.'}
      >
        {all.length > 5 && <SearchField value={query} onChange={setQuery} placeholder="Search people" hotkey="/" width={240} />}
        <Gate allow={owner} reason={onlyOwner('invite people')}>
          <button className="btn primary" onClick={() => setInviting(true)}>
            Invite someone
          </button>
        </Gate>
      </PageHeading>
      {people.error && <Problem>{people.error.message}</Problem>}
      {!people.data && !people.error && (
        <div className="panel">
          <Skeleton rows={3} tall />
        </div>
      )}
      {people.data && (
        <div className="stack">
          <section className="panel">
            {paged.shown.map((m) => (
              <div key={m.id} className="activity-row people">
                <div className="event">
                  <i className={'swatch ' + (activeNow(m.last_active_at, now) ? 'done' : '')} />
                  <div>
                    <span className="t-small">
                      <span className="t-strong">{m.name || m.email}</span>
                      {m.you && <span className="t-muted"> (you)</span>}
                    </span>
                    <span className="t-note">{m.email}</span>
                  </div>
                </div>
                <span className="t-caption t-muted">{roleName[m.role]}</span>
                {activeNow(m.last_active_at, now) ? (
                  <span className="t-note when">Active now</span>
                ) : (
                  <span className="t-note when">
                    Last active <When at={m.last_active_at} now={now} lower />
                  </span>
                )}
                {owner && m.role !== 'owner' ? (
                  <span className="row-more">
                    <Menu label={`Actions for ${who(m)}`} items={actions(m)} className="icon-btn bare" />
                  </span>
                ) : (
                  <span />
                )}
              </div>
            ))}
            {found.length === 0 && (
              <div className="t-empty">
                <span className="t-caption t-muted">Nobody matches “{query.trim()}”.</span>
                <button className="btn quiet small" onClick={() => setQuery('')}>
                  Show everyone
                </button>
              </div>
            )}
          </section>
          <Paging paged={paged} noun="person" many="people" />
        </div>
      )}
      {owner && waiting.length > 0 && (
        <div className="stack">
          <div className="toolbar">
            <div className="title">
              <h2 className="t-small t-strong">Invites not used yet</h2>
              <span className="count">{waiting.length}</span>
            </div>
          </div>
          <section className="panel">
            {waiting.map((inv) => (
              <div key={inv.id} className="activity-row people">
                <div className="event">
                  <i className="swatch" />
                  <div>
                    <span className="t-small">An invite to join as {inv.role === 'member' ? 'a member' : 'a viewer'}</span>
                    <span className="t-note">
                      Made by {inv.created_by} <When at={inv.created_at} now={now} lower />.
                    </span>
                  </div>
                </div>
                <span className="t-caption t-muted">{roleName[inv.role]}</span>
                <span className="t-note when" data-tip={new Date(inv.expires_at).toLocaleString('en-GB')}>
                  Works until {new Date(inv.expires_at).toLocaleDateString('en-GB', { day: 'numeric', month: 'short' })}
                </span>
                <span className="row-more">
                  <Menu
                    label="Actions for this invite"
                    className="icon-btn bare"
                    items={[{ label: 'Revoke invite', icon: <Trash />, danger: true, onSelect: () => revoke(inv) }]}
                  />
                </span>
              </div>
            ))}
          </section>
        </div>
      )}
      <YourSessions now={now} />
      {inviting && (
        <InviteDialog
          onClose={() => {
            setInviting(false)
            invites.reload()
          }}
          onMade={() => invites.reload()}
        />
      )}
    </main>
  )
}

// InviteDialog asks what the person will be able to do, then gives the
// link to send them. Pier doesn't send email, so the owner sends it.
function InviteDialog({ onClose, onMade }: { onClose: () => void; onMade: () => void }) {
  const [role, setRole] = useState<'member' | 'viewer'>('member')
  const [made, setMade] = useState<{ link: string; invite: Invite } | null>(null)
  const [busy, setBusy] = useState(false)
  const [problem, setProblem] = useState('')

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (made) return copy(made.link, 'the invite link')
    setBusy(true)
    setProblem('')
    try {
      setMade(await api<{ link: string; invite: Invite }>('/api/invites', { method: 'POST', body: JSON.stringify({ role }) }))
      onMade()
    } catch (err) {
      setProblem(err instanceof Error ? err.message : "Can't reach pierd.")
    } finally {
      setBusy(false)
    }
  }

  if (made) {
    const until = new Date(made.invite.expires_at).toLocaleDateString('en-GB', { weekday: 'long', day: 'numeric', month: 'long' })
    const as = made.invite.role === 'member' ? 'a member' : 'a viewer'
    return (
      <Dialog
        wide
        title={`An invite to join as ${as}`}
        onClose={onClose}
        onSubmit={submit}
        footer={
          <>
            <button className="btn quiet medium" type="button" onClick={onClose}>
              Done
            </button>
            <button className="btn primary medium" type="submit">
              Copy link
            </button>
          </>
        }
      >
        <p>
          Send this link to the person you're inviting. It works once, until {until}, and whoever opens it first joins as {as}.
          This is the only time it's shown.
        </p>
        <label className="field">
          <span className="field-label">Invite link</span>
          <input className="input mono" readOnly value={made.link} onClick={(e) => e.currentTarget.select()} />
        </label>
      </Dialog>
    )
  }

  return (
    <Dialog
      wide
      title="Invite someone"
      busy={busy}
      onClose={onClose}
      onSubmit={submit}
      footer={
        <>
          {problem && (
            <span className="grow field-message" role="alert">
              {problem}
            </span>
          )}
          <button className="btn quiet medium" type="button" disabled={busy} onClick={onClose}>
            Cancel
          </button>
          <button className="btn primary medium" type="submit" disabled={busy}>
            {busy && <Spinner />}
            {busy ? 'Making the link…' : 'Make an invite link'}
          </button>
        </>
      }
    >
      <div className="field">
        <span className="field-label">They join as</span>
        <div className="role-choices" role="radiogroup" aria-label="They join as">
          {(['member', 'viewer'] as const).map((r) => (
            <label key={r} className="check role-choice">
              <input type="radio" name="role" checked={role === r} onChange={() => setRole(r)} autoFocus={r === 'member'} />
              <span>{roleName[r]}</span>
              <span className="t-note">{roleDoes[r]}</span>
            </label>
          ))}
        </div>
        <span className="t-note">
          The link works once, for 7 days. Pier doesn't send email, so you send it yourself.
        </span>
      </div>
    </Dialog>
  )
}

// YourSessions is every browser and computer signed in as you, each of
// which can be signed out.
function YourSessions({ now }: { now: number }) {
  const { data, error, reload } = useData<Sessions>('/api/sessions')
  const sessions = data?.sessions ?? []
  const paged = usePaged(sessions, PAGE, 0)

  const signOut = (s: SessionRow) => {
    const name = s.name || (s.kind === 'cli' ? 'pier CLI' : 'A browser')
    return confirm({
      title: `Sign out ${name}?`,
      body: (
        <p>
          {s.kind === 'cli'
            ? 'pier stops working on that computer until someone runs pier login there again and you approve it.'
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
          Every other browser goes back to the sign-in page, and pier stops working on every computer you approved until it's
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

  if (error) return <Problem>{error.message}</Problem>
  if (!data) return null
  return (
    <div className="stack" id="sessions">
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
        {paged.shown.map((s) => (
          <div key={s.id} className="activity-row sessions">
            <div className="event">
              <i className={'swatch ' + (s.current ? 'done' : '')} />
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
            {s.current || activeNow(s.last_seen_at, now) ? (
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
        ))}
      </section>
      <Paging paged={paged} noun="session" />
    </div>
  )
}
