import { useState } from 'react'
import { api, ApiError, type LoginRequest } from '../lib/api'
import { useData } from '../lib/data'
import { ago } from '../lib/format'
import { Link } from '../lib/router'
import { Mark } from '../components/Icons'

// Approve lets a computer that ran pier login in: it shows the computer's
// name and the code the terminal printed, so the owner can check they match.
export function Approve({ code, now }: { code: string; now: number }) {
  const { data, error } = useData<LoginRequest>(code ? `/api/cli/login/${encodeURIComponent(code)}` : null)
  const [answer, setAnswer] = useState<'approved' | 'denied' | ''>('')
  const [failed, setFailed] = useState('')
  const [busy, setBusy] = useState(false)

  const send = async (a: 'approve' | 'deny') => {
    setBusy(true)
    setFailed('')
    try {
      await api(`/api/cli/login/${encodeURIComponent(code)}/${a}`, { method: 'POST' })
      setAnswer(a === 'approve' ? 'approved' : 'denied')
    } catch (e) {
      setFailed(e instanceof ApiError ? e.message : "Can't reach pierd.")
    } finally {
      setBusy(false)
    }
  }

  let body: React.ReactNode
  if (!code) {
    body = <p className="t-caption t-muted">This page needs the link pier login printed.</p>
  } else if (error) {
    body = <p className="t-caption t-muted">{error.message}</p>
  } else if (!data) {
    body = <p className="t-note">Loading…</p>
  } else if (answer === 'approved' || data.status === 'approved') {
    body = (
      <p className="t-caption t-muted">
        Approved. The terminal is signed in now, and this computer shows on the{' '}
        <Link to="/members" style={{ color: 'var(--d-text)' }}>
          Members
        </Link>{' '}
        page, where you can sign it out.
      </p>
    )
  } else if (answer === 'denied' || data.status === 'denied') {
    body = <p className="t-caption t-muted">Denied. The terminal says so, and nothing was signed in.</p>
  } else {
    body = (
      <>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <span className="t-small t-strong">pier CLI on {data.name}</span>
          <span className="t-note">
            From {data.ip}, {ago(data.created_at, now).toLowerCase()}
          </span>
        </div>
        <div className="code-box">
          <span className="t-note">The terminal's code</span>
          <span className="mono t-title">{data.code}</span>
        </div>
        <p className="t-caption t-muted" style={{ margin: 0 }}>
          Check it matches what pier login printed. Once approved, this computer can deploy to every project as you.
        </p>
        {failed && (
          <span className="field-message" role="alert">
            {failed}
          </span>
        )}
        <div style={{ display: 'flex', gap: 10 }}>
          <button className="btn primary large" style={{ flex: 1 }} disabled={busy} onClick={() => send('approve')}>
            Approve
          </button>
          <button className="btn quiet large" disabled={busy} onClick={() => send('deny')}>
            Deny
          </button>
        </div>
      </>
    )
  }

  return (
    <main className="sign-in">
      <div className="focus-card">
        <div style={{ display: 'flex', flexDirection: 'column', gap: 18 }}>
          <Mark size={32} />
          <h1 className="t-title" style={{ margin: 0 }}>
            Approve this computer
          </h1>
        </div>
        {body}
      </div>
    </main>
  )
}
