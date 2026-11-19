import { GitHubRepos } from './GitHub'
import { useState, type FormEvent } from 'react'
import { api, ApiError, type Session } from '../lib/api'
import { navigate } from '../lib/router'

const nameRule = /^[a-z0-9]([a-z0-9-]{0,37}[a-z0-9])?$/

// D1 offers a GitHub import or an empty project for the CLI.
export function NewProject({ session, onCreated }: { session: Session; onCreated: () => void }) {
  const [name, setName] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const domain = session.server.domain
  const shown = name || 'my-site'

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    const n = name.trim()
    if (!nameRule.test(n) || n.includes('--')) {
      setError('Use lowercase letters, numbers and single dashes, like my-site.')
      return
    }
    setBusy(true)
    setError('')
    try {
      await api('/api/projects', { method: 'POST', body: JSON.stringify({ name: n }) })
      onCreated()
      navigate(`/projects/${n}`)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Can't reach pierd.")
    } finally {
      setBusy(false)
    }
  }

  return (
    <main className="main global" style={{ gap: 24 }}>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
        <h1 className="t-title" style={{ margin: 0 }}>
          New project
        </h1>
        <span className="t-caption t-muted">Pick where the code comes from. Pier builds it, and keeps every deployment.</span>
      </div>
      <div className="columns">
        <section className="panel" style={{ flex: 1, minWidth: 0 }}>
          <div className="panel-head">
            <GitHubMark />
            <span className="t-small t-strong">Import from GitHub</span>
          </div>
          <div style={{ padding: '20px', display: 'flex', flexDirection: 'column', gap: 6 }}>
            <GitHubRepos />
          </div>
        </section>
        <form className="panel cli-panel" onSubmit={submit} noValidate>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            <span className="t-small t-strong">Deploy from your computer</span>
            <span className="t-caption t-muted">Start with an empty project, then run pier deploy in any folder. No GitHub needed.</span>
          </div>
          <label className={'field' + (error ? ' error' : '')}>
            <span className="field-label">Project name</span>
            <input
              className="input mono"
              placeholder="my-site"
              value={name}
              autoFocus
              spellCheck={false}
              autoCapitalize="off"
              onChange={(e) => {
                setName(e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, '-'))
                setError('')
              }}
            />
            {error ? (
              <span className="field-message" role="alert">
                {error}
              </span>
            ) : (
              <span className="t-note" style={{ color: 'var(--d-muted)' }}>
                Lowercase letters, numbers and dashes. It becomes {shown}.{domain}.
              </span>
            )}
          </label>
          <button className="btn primary large" type="submit" disabled={busy}>
            {busy ? 'Creating…' : 'Create empty project'}
          </button>
        </form>
      </div>
    </main>
  )
}

export const GitHubMark = ({ size = 20 }: { size?: number }) => (
  <svg viewBox="0 0 24 24" fill="#ECEEF4" width={size} height={size} aria-hidden>
    <path d="M12 .297c-6.63 0-12 5.373-12 12 0 5.303 3.438 9.8 8.205 11.385.6.113.82-.258.82-.577 0-.285-.01-1.04-.015-2.04-3.338.724-4.042-1.61-4.042-1.61C4.422 18.07 3.633 17.7 3.633 17.7c-1.087-.744.084-.729.084-.729 1.205.084 1.838 1.236 1.838 1.236 1.07 1.835 2.809 1.305 3.495.998.108-.776.417-1.305.76-1.605-2.665-.3-5.466-1.332-5.466-5.93 0-1.31.465-2.38 1.235-3.22-.135-.303-.54-1.523.105-3.176 0 0 1.005-.322 3.3 1.23.96-.267 1.98-.399 3-.405 1.02.006 2.04.138 3 .405 2.28-1.552 3.285-1.23 3.285-1.23.645 1.653.24 2.873.12 3.176.765.84 1.23 1.91 1.23 3.22 0 4.61-2.805 5.625-5.475 5.92.42.36.81 1.096.81 2.22 0 1.606-.015 2.896-.015 3.286 0 .315.21.69.825.57C20.565 22.092 24 17.592 24 12.297c0-6.627-5.373-12-12-12" />
  </svg>
)
