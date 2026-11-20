import { useState, type FormEvent } from 'react'
import { api, ApiError, type Session } from '../lib/api'
import { Link, navigate } from '../lib/router'
import { ChevronRight, Terminal } from '../components/Icons'
import { GitHubRepos } from './GitHub'

const nameRule = /^[a-z0-9]([a-z0-9-]{0,37}[a-z0-9])?$/

// NewProject is D1: import a repo from GitHub, or make an empty project to
// deploy into from a computer.
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
    <main className="main global" style={{ gap: 28 }}>
      <div className="stack">
        <nav className="breadcrumb" aria-label="Breadcrumb">
          <Link to="/">Projects</Link>
          <ChevronRight />
          <span style={{ color: 'var(--d-text)' }}>New project</span>
        </nav>
        <header className="page-heading">
          <div>
            <h1 className="t-title">New project</h1>
            <p className="t-caption t-muted">Pick where the code comes from. Pier builds it, and keeps every deployment.</p>
          </div>
        </header>
      </div>

      <GitHubRepos />

      <div className="stack">
        <div className="toolbar">
          <div className="title">
            <Terminal size={20} color="#ECEEF4" />
            <h2 className="t-small t-strong">Deploy from your computer</h2>
          </div>
        </div>
        <form className="panel form-panel" onSubmit={submit} noValidate>
          <p className="t-caption t-muted">
            Start with an empty project, then run <span className="mono">pier deploy</span> in any folder. No GitHub needed.
          </p>
          <div className="inline-form">
            <label className={'field' + (error ? ' error' : '')}>
              <span className="field-label">Project name</span>
              <input
                className="input mono"
                placeholder="my-site"
                value={name}
                spellCheck={false}
                autoCapitalize="off"
                autoComplete="off"
                onChange={(e) => {
                  setName(e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, '-'))
                  setError('')
                }}
              />
            </label>
            <button className="btn quiet large" type="submit" disabled={busy || !name}>
              {busy ? 'Creating…' : 'Create empty project'}
            </button>
          </div>
          {error ? (
            <span className="field-message" role="alert">
              {error}
            </span>
          ) : (
            <span className="t-note">
              Lowercase letters, numbers and dashes. It becomes {shown}.{domain}.
            </span>
          )}
        </form>
      </div>
    </main>
  )
}
