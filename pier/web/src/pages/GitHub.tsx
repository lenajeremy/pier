import { useState, type FormEvent } from 'react'
import { api, type Build } from '../lib/api'
import { useData } from '../lib/data'
import { Link, navigate } from '../lib/router'

type Repo = {
  id: number
  full_name: string
  default_branch: string
  private: boolean
  installation: number
  project?: string
}
const message = (e: unknown) => (e instanceof Error ? e.message : 'Could not reach Pier.')
const githubPage = '/api/github'
const defaults: Build = { root: '', preset: '', install: '', command: '', output: '' }

export function GitHubRepos() {
  const { data, error } = useData<{ repositories: Repo[] }>(githubPage + '/repositories')
  if (error) return <span role="alert">{error.message}</span>
  if (!data) return <span className="t-note">Loading repositories…</span>
  if (!data.repositories.length)
    return (
      <span className="t-caption">
        Install the App on the repositories you want to deploy. <Link to="/integrations">GitHub settings</Link>
      </span>
    )
  return (
    <div>
      {data.repositories.map((r) => (
        <div className="github-row" key={r.id}>
          <span className="t-caption">
            {r.full_name} <span className="t-note">{r.private ? 'Private' : 'Public'}</span>
          </span>
          <Link className="btn quiet" to={r.project ? '/projects/' + r.project : '/new/github/' + r.id}>
            {r.project ? 'View project' : 'Import'}
          </Link>
        </div>
      ))}
    </div>
  )
}

export function GitHubImport({ id, onCreated }: { id: string; onCreated: () => void }) {
  const { data, error } = useData<{ repositories: Repo[] }>(githubPage + '/repositories')
  const repo = data?.repositories.find((r) => String(r.id) === id)
  if (error)
    return (
      <main className="main">
        <p role="alert">{error.message}</p>
      </main>
    )
  if (!repo) return <main className="main">{data ? 'This repository is unavailable.' : 'Loading repository…'}</main>
  return <Configure key={id} repo={repo} onCreated={onCreated} />
}
function Configure({ repo, onCreated }: { repo: Repo; onCreated: () => void }) {
  const [name, setName] = useState(
    repo.full_name
      .split('/')[1]
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, '-')
      .slice(0, 39)
      .replace(/^-|-$/g, ''),
  )
  const [branch, setBranch] = useState(repo.default_branch)
  const [build, setBuild] = useState<Build>(defaults)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      await api(githubPage + '/import', {
        method: 'POST',
        body: JSON.stringify({ name, branch, build, repo: repo.id, installation: repo.installation }),
      })
      onCreated()
      navigate('/projects/' + name)
    } catch (e) {
      setError(message(e))
      setBusy(false)
    }
  }
  return (
    <main className="main global">
      <h1 className="t-title">Configure {repo.full_name}</h1>
      <form className="panel form-panel github-fields" onSubmit={submit}>
        <label className="field">
          <span className="field-label">Project name</span>
          <input className="input" value={name} onChange={(e) => setName(e.target.value)} required />
        </label>
        <label className="field">
          <span className="field-label">Production branch</span>
          <input className="input mono" value={branch} onChange={(e) => setBranch(e.target.value)} required />
        </label>
        <h2 className="t-small">Build</h2>
        <label className="field">
          <span className="field-label">Preset</span>
          <select
            className="input"
            value={build.preset}
            onChange={(e) => setBuild({ ...build, preset: e.target.value as Build['preset'] })}
          >
            <option value="">Detect automatically</option>
            <option value="static">Static HTML</option>
            <option value="vite">Vite</option>
          </select>
        </label>
        <div className="form-grid">
          {(
            [
              ['root', 'Root directory'],
              ['install', 'Install command'],
              ['command', 'Build command'],
              ['output', 'Output folder'],
            ] as const
          ).map(([key, label]) => (
            <label className="field" key={key}>
              <span className="field-label">{label}</span>
              <input
                className="input mono"
                value={build[key]}
                placeholder="Automatic"
                onChange={(e) => setBuild({ ...build, [key]: e.target.value })}
              />
            </label>
          ))}
        </div>
        {error && (
          <span role="alert" className="field-message">
            {error}
          </span>
        )}
        <div>
          <button disabled={busy} className="btn primary">
            {busy ? 'Importing…' : 'Deploy ' + name}
          </button>
        </div>
      </form>
    </main>
  )
}
