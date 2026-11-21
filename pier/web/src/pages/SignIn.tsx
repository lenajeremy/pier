import { useState, type FormEvent } from 'react'
import { ApiError, signIn, type Session } from '../lib/api'
import { Mark } from '../components/Icons'
import { Spinner } from '../components/Spinner'

// SignIn is B1: the owner's email and password. Pier never sends email, so
// a forgotten password is reset on the server.
export function SignIn({ address, onSignedIn }: { address: string; onSignedIn: (s: Session) => void }) {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (!email || !password) {
      setError('Type your email and password.')
      return
    }
    setBusy(true)
    setError('')
    try {
      onSignedIn(await signIn(email, password))
    } catch (err) {
      if (err instanceof ApiError) {
        const left = err.body.tries_left
        setError(
          typeof left === 'number'
            ? `${err.message} ${left === 1 ? 'One more try' : `${left} more tries`} before it locks for a minute.`
            : err.message,
        )
      } else {
        setError("Can't reach pierd. Check that it's running.")
      }
      setPassword('')
    } finally {
      setBusy(false)
    }
  }

  return (
    <main className="sign-in">
      <form onSubmit={submit} noValidate>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 18 }}>
          <Mark size={32} />
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            <h1 className="t-title" style={{ margin: 0 }}>
              Sign in
            </h1>
            <span className="mono t-caption t-muted">{address || location.host}</span>
          </div>
        </div>
        <div className="fields">
          <label className={'field' + (error ? ' error' : '')}>
            <span className="field-label">Email</span>
            <input
              className="input"
              type="email"
              autoComplete="username"
              autoFocus
              value={email}
              onChange={(e) => setEmail(e.target.value)}
            />
          </label>
          <label className={'field' + (error ? ' error' : '')}>
            <span className="field-label">Password</span>
            <input
              className="input"
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
            {error && (
              <span className="field-message" role="alert">
                {error}
              </span>
            )}
          </label>
        </div>
        <button className="btn primary large" type="submit" disabled={busy}>
          {busy && <Spinner />}
          {busy ? 'Signing in…' : 'Sign in'}
        </button>
        <p className="forgot">
          Forgot your password? Run <span className="mono t-muted">pierd reset-password</span> on the server.
        </p>
      </form>
    </main>
  )
}
