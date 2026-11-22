import { useEffect, useState, type FormEvent } from 'react'
import { api, ApiError, type Session } from '../lib/api'
import { Mark } from '../components/Icons'
import { Spinner } from '../components/Spinner'
import { navigate } from '../lib/router'

type Found = { role: 'member' | 'viewer'; invited_by: string; expires_at: string; server: string }

const roleDoes = {
  member: 'you can deploy, promote, roll back and change every project',
  viewer: 'you can look at every project, its deployments and its logs, but not change anything',
}

// Invite is the page an invite link opens: who invited you and as what,
// then your name, email and password, which make your account and sign you
// in. The token is in the link's fragment, so it never reaches a server log.
export function Invite({ signedIn, onJoined }: { signedIn: Session | null; onJoined: (s: Session) => void }) {
  const token = location.hash.slice(1)
  const [found, setFound] = useState<Found | null>(null)
  const [gone, setGone] = useState('')
  const [name, setName] = useState('')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [problem, setProblem] = useState<{ field: string; message: string } | null>(null)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    api<Found>('/api/invites/lookup', { method: 'POST', body: JSON.stringify({ token }) })
      .then(setFound)
      .catch((e) => setGone(e instanceof ApiError ? e.message : "Can't reach pierd. Check that it's running."))
  }, [token])

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setProblem(null)
    try {
      const s = await api<Session>('/api/invites/accept', {
        method: 'POST',
        body: JSON.stringify({ token, name: name.trim(), email: email.trim(), password }),
      })
      navigate('/', true)
      onJoined(s)
    } catch (err) {
      if (err instanceof ApiError && err.status === 410) {
        setGone(err.message)
      } else if (err instanceof ApiError) {
        setProblem({ field: typeof err.body.field === 'string' ? err.body.field : 'password', message: err.message })
      } else {
        setProblem({ field: 'password', message: "Can't reach pierd. Check that it's running." })
      }
      setBusy(false)
    }
  }

  const message = (field: string) =>
    problem?.field === field && (
      <span className="field-message" role="alert">
        {problem.message}
      </span>
    )

  if (gone) {
    return (
      <main className="sign-in">
        <div className="focus-card">
          <Mark size={32} />
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            <h1 className="t-title" style={{ margin: 0 }}>
              This invite doesn't work
            </h1>
            <span className="t-caption t-muted">{gone}</span>
          </div>
          <a className="btn quiet large" href="/">
            Go to sign in
          </a>
        </div>
      </main>
    )
  }
  if (!found) {
    return (
      <main className="sign-in">
        <Spinner large label="Opening the invite" />
      </main>
    )
  }

  const as = found.role === 'member' ? 'a member' : 'a viewer'
  const until = new Date(found.expires_at).toLocaleDateString('en-GB', { day: 'numeric', month: 'long' })
  return (
    <main className="sign-in">
      <form onSubmit={submit} noValidate>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 18 }}>
          <Mark size={32} />
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            <h1 className="t-title" style={{ margin: 0 }}>
              Join {found.invited_by}'s Pier
            </h1>
            <span className="mono t-caption t-muted">{found.server || location.host}</span>
          </div>
          <p className="t-caption t-muted" style={{ margin: 0 }}>
            {found.invited_by} invited you as {as}: {roleDoes[found.role]}. Make your account to start.
          </p>
        </div>
        <div className="fields">
          <label className={'field' + (problem?.field === 'name' ? ' error' : '')}>
            <span className="field-label">Name</span>
            <input className="input" autoComplete="name" autoFocus value={name} onChange={(e) => setName(e.target.value)} />
            {message('name') || <span className="t-note">Shown beside what you deploy and change.</span>}
          </label>
          <label className={'field' + (problem?.field === 'email' ? ' error' : '')}>
            <span className="field-label">Email</span>
            <input className="input" type="email" autoComplete="username" value={email} onChange={(e) => setEmail(e.target.value)} />
            {message('email')}
          </label>
          <label className={'field' + (problem?.field === 'password' ? ' error' : '')}>
            <span className="field-label">Password</span>
            <input
              className="input"
              type="password"
              autoComplete="new-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
            {message('password') || <span className="t-note">12 characters or more.</span>}
          </label>
        </div>
        <button className="btn primary large" type="submit" disabled={busy || !name.trim() || !email.trim() || !password}>
          {busy && <Spinner />}
          {busy ? 'Making your account…' : `Join as ${as}`}
        </button>
        <p className="forgot">
          {signedIn?.user
            ? `This browser is signed in as ${signedIn.user.email}. Joining signs it in as your new account instead.`
            : `The link works once, until ${until}.`}
        </p>
      </form>
    </main>
  )
}
