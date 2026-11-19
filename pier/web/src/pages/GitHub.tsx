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
type Job = { id: number; project: string; pr: number; sha: string; state: string; detail: string }
type Info = { slug: string; hook: string; jobs: Job[] }

const message = (e: unknown) => (e instanceof Error ? e.message : 'Could not reach Pier.')
const githubPage = '/api/github'
const defaults: Build = { root: '', preset: '', install: '', command: '', output: '' }

export function Integrations() {
  const { data, error, reload } = useData<Info>(githubPage, () => true, 10000)
  const [hook, setHook] = useState('https://' + location.host + '/github/events')
  const [failure, setFailure] = useState('')
  const [busy, setBusy] = useState(false)
  async function setup(event: FormEvent) {
    event.preventDefault()
    setBusy(true)
    setFailure('')
    try {
      const setup = await api<{ action: string; manifest: object }>(githubPage + '/manifest', {
        method: 'POST',
        body: JSON.stringify({ hook }),
      })
      const form = document.createElement('form')
      form.method = 'POST'
      form.action = setup.action
      const manifest = document.createElement('input')
      manifest.type = 'hidden'
      manifest.name = 'manifest'
      manifest.value = JSON.stringify(setup.manifest)
      form.append(manifest)
      document.body.append(form)
      form.submit()
    } catch (e) {
      setFailure(message(e))
      setBusy(false)
    }
  }
  async function resume(id: number) {
    setFailure('')
    try {
      await api(githubPage + '/jobs/' + id + '/resume', { method: 'POST' })
      await reload()
    } catch (e) {
      setFailure(message(e))
    }
  }
  return (
    <main className="main global">
      <h1 className="t-title">Integrations</h1>
      <section className="panel form-panel">
        <h2 className="t-small">GitHub</h2>
        {error && <p role="alert">{error.message}</p>}
        {data &&
          (data.slug ? (
            <>
              <span className="t-caption">App registered as {data.slug}</span>
              <span className="mono t-note">{data.hook}</span>
              <div>
                <a className="btn quiet" href={'https://github.com/apps/' + data.slug + '/installations/new'}>
                  Manage on GitHub
                </a>
              </div>
            </>
          ) : (
            <form className="github-fields" onSubmit={setup}>
              <p className="t-caption">
                Create a GitHub App in your account. It reads the repositories you select, receives pushes and pull
                requests, and posts build statuses and preview links.
              </p>
              <label className="field">
                <span className="field-label">Webhook URL</span>
                <input
                  type="url"
                  className="input mono"
                  required
                  value={hook}
                  onChange={(e) => setHook(e.target.value)}
                />
                <span className="t-note">
                  GitHub delivers events here. Use the public HTTPS address of your server or tunnel.
                </span>
              </label>
              <div>
                <button className="btn primary" disabled={busy}>
                  Create GitHub App
                </button>
              </div>
            </form>
          ))}
      </section>
      {failure && (
        <p role="alert" className="field-message">
          {failure}
        </p>
      )}
      {data?.slug && (
        <section className="panel form-panel">
          <h2 className="t-small">Repositories Pier can see</h2>
          <GitHubRepos />
        </section>
      )}
      {!!data?.jobs.length && (
        <section className="panel form-panel">
          <h2 className="t-small">Deployments needing attention</h2>
          {data.jobs.map((j) => (
            <div className="github-row" key={j.id}>
              <div>
                <strong>{j.project}</strong> {j.pr > 0 && ' · PR #' + j.pr} <code>{j.sha.slice(0, 7)}</code>
                <p className="t-note">
                  {j.state === 'approval'
                    ? 'This commit comes from a fork. Approving it runs its code with this project’s preview variables.'
                    : j.detail}
                </p>
              </div>
              <button className="btn quiet" onClick={() => resume(j.id)}>
                {j.state === 'approval' ? 'Approve this commit' : 'Retry'}
              </button>
            </div>
          ))}
        </section>
      )}
    </main>
  )
}

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
