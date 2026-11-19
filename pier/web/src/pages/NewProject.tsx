import { GitHubMark } from '../components/Icons'
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
