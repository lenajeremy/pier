import { useState, type FormEvent } from 'react'
import { api } from '../lib/api'
import { useData, useNow } from '../lib/data'
import { ago } from '../lib/format'
import { Link } from '../lib/router'
import { Check, Docs, GitHubMark, Visit } from '../components/Icons'

type Job = { id: number; project: string; pr: number; sha: string; state: string; detail: string }
type Info = { slug: string; hook: string; jobs: Job[] }
type Installation = { id: number; account: { login: string; type: string }; html_url: string }
type Repo = { id: number; full_name: string; private: boolean; language: string; pushed_at: string; project?: string }
type Catalog = { repositories: Repo[]; installations: Installation[] }
type Delivery = { delivered_at: string; event: string; status_code: number; duration: number }
type Activity = { created_at: string; deliveries: Delivery[] }
const permissions = [
  ['Read the code in the repos you pick', 'contents: read'],
  ['Hear about pushes and pull requests', 'push, pull_request events'],
  ['Post the preview link on pull requests', 'pull requests: write'],
  ['Show each build’s status on its commit', 'commit statuses: write'],
]
const message = (err: unknown) => (err instanceof Error ? err.message : 'Could not reach Pier.')

export function Integrations() {
  const { data, error, reload } = useData<Info>('/api/github', () => true, 10000)
  const [failure, setFailure] = useState('')
  const catalog = useData<Catalog>(data?.slug ? '/api/github/repositories' : null)
  const activity = useData<Activity>(data?.slug ? '/api/github/activity' : null, () => true, 60000)
  const now = useNow()
  const installs = catalog.data?.installations ?? []
  const installed = installs.length > 0
  const repos = catalog.data?.repositories ?? []
  const managed = data?.slug ? 'https://github.com/settings/apps/' + data.slug : ''
  const installURL = 'https://github.com/apps/' + data?.slug + '/installations/new'
  const last = activity.data?.deliveries[0]
  const hook = data?.hook ? new URL(data.hook) : null
  const installedLabel = installs.map((i) => i.account.login).join(', ')
  const status = !data?.slug
    ? 'Not connected'
    : !catalog.data
      ? catalog.error
        ? 'Connection unavailable'
        : 'Checking installation…'
      : installed
        ? 'Connected'
        : 'Awaiting installation'
  async function resume(id: number) {
    setFailure('')
    try {
      await api('/api/github/jobs/' + id + '/resume', { method: 'POST' })
      await reload()
    } catch (e) {
      setFailure(message(e))
    }
  }
  return (
    <main className="main global integrations-page">
      <header className="integrations-title">
        <h1 className="t-title">Integrations</h1>
        <p className="t-caption t-muted">The outside services Pier works with.</p>
      </header>
      {error && (
        <p role="alert" className="field-message">
          {error.message}
        </p>
      )}
      {data && (
        <section className="panel integration-card" aria-label="GitHub connection">
          <header className="integration-head">
            <span className="integration-mark">
              <GitHubMark size={22} />
            </span>
            <div className="integration-identity">
              <h2 className="t-small t-strong">GitHub</h2>
              <p className="t-caption t-muted">Import repos, and deploy every push and pull request.</p>
            </div>
            <span className="integration-state t-caption">
              <i className={'integration-swatch' + (installed ? ' healthy' : '')} />
              {status}
            </span>
            {data.slug && (
              <a className="btn quiet" href={catalog.data && !installed ? installURL : managed}>
                {catalog.data && !installed ? 'Install on GitHub' : 'Manage on GitHub'}
                <Visit size={14} />
              </a>
            )}
          </header>
          {data.slug && hook ? (
            <dl className="integration-summary">
              <div>
                <dt>App</dt>
                <dd className="mono">{data.slug}</dd>
                {activity.data?.created_at && (
                  <small>
                    Created{' '}
                    {new Date(activity.data.created_at).toLocaleDateString('en-GB', {
                      day: 'numeric',
                      month: 'short',
                      year: 'numeric',
                    })}
                  </small>
                )}
              </div>
              <div>
                <dt>Installed on</dt>
                <dd>{installedLabel || (catalog.data ? 'No installations' : '—')}</dd>
                {installed && (
                  <small>
                    {installs.length === 1
                      ? installs[0].account.type === 'Organization'
                        ? 'Organization'
                        : 'Personal account'
                      : installs.length + ' accounts'}{' '}
                    · {repos.length} {repos.length === 1 ? 'repo' : 'repos'}
                  </small>
                )}
              </div>
              <div>
                <dt>Events go to</dt>
                <dd className="integration-endpoint">
                  <span className="mono">{hook.pathname}</span>
                  {!!activity.data?.deliveries.length && <Durations deliveries={activity.data.deliveries} />}
                </dd>
                <small title={hook.host}>On {hook.host}</small>
              </div>
              <div>
                <dt>Last event</dt>
                <dd className="integration-state">
                  {last && (
                    <i
                      className={
                        'integration-swatch ' +
                        (last.status_code >= 200 && last.status_code < 300 ? 'healthy' : 'failed')
                      }
                    />
                  )}
                  {last
                    ? ago(last.delivered_at, now)
                    : activity.error
                      ? 'Unavailable'
                      : activity.data
                        ? 'No recent events'
                        : '—'}
                </dd>
                {last && (
                  <small>
                    {last.event.replaceAll('_', ' ')} ·{' '}
                    {last.status_code ? 'HTTP ' + last.status_code : 'Delivery failed'}
                  </small>
                )}
                {activity.error && <small>{activity.error.message}</small>}
              </div>
            </dl>
          ) : (
            <Connect />
          )}
        </section>
      )}
      {failure && (
        <p role="alert" className="field-message">
          {failure}
        </p>
      )}
      {!!data?.jobs.length && (
        <section className="integration-section">
          <h2 className="t-small t-strong">Deployments needing attention</h2>
          <div className="panel">
            {data.jobs.map((j) => (
              <div className="integration-approval" key={j.id}>
                <div>
                  <span className="t-small t-strong">
                    {j.project}
                    {j.pr > 0 && ' · PR #' + j.pr}
                  </span>{' '}
                  <code className="t-caption mono t-muted">{j.sha.slice(0, 7)}</code>
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
          </div>
        </section>
      )}
      {data?.slug && (
        <section className="integration-section">
          <header>
            <h2 className="t-small t-strong">Repos Pier can see</h2>
            {installed && (
              <a className="t-caption" href={installs.length === 1 ? installs[0].html_url : installURL}>
                Change on GitHub <Visit size={13} />
              </a>
            )}
          </header>
          <div className="panel integration-repos">
            {repos.map((r) => (
              <div className="integration-repo" key={r.id}>
                <Docs size={16} />
                <div className="integration-repo-name">
                  <span className="t-small t-strong" title={r.full_name}>
                    {installs.length > 1 ? r.full_name : r.full_name.split('/').pop()}
                  </span>
                  <small className="t-note">{r.private ? 'Private' : 'Public'}</small>
                </div>
                <span className="repo-language t-note">{r.language}</span>
                <time className="repo-time t-note" title={r.pushed_at}>
                  {r.pushed_at ? ago(r.pushed_at, now) : ''}
                </time>
                <Link className="btn quiet" to={r.project ? '/projects/' + r.project : '/new/github/' + r.id}>
                  {r.project ? 'View project' : 'Import'}
                </Link>
              </div>
            ))}
            {!repos.length && (
              <p className="integration-empty t-caption t-muted">
                {catalog.error
                  ? catalog.error.message
                  : !catalog.data
                    ? 'Loading repositories…'
                    : installed
                      ? 'No repositories selected.'
                      : 'Install the App to select repositories.'}
              </p>
            )}
          </div>
        </section>
      )}
    </main>
  )
}

function Durations({ deliveries }: { deliveries: Delivery[] }) {
  const recent = [...deliveries].reverse()
  const max = Math.max(...recent.map((d) => d.duration), 0.001)
  return (
    <span className="bars integration-bars" aria-label="Recent webhook delivery durations">
      {recent.map((d, i) => (
        <i
          key={i}
          className={d.status_code >= 400 ? 'failed' : i === recent.length - 1 ? 'newest' : ''}
          title={d.event + ' · ' + Math.round(d.duration * 1000) + ' ms'}
          style={{ height: Math.max(3, Math.round((d.duration / max) * 23)) }}
        />
      ))}
    </span>
  )
}

function Connect() {
  const [hook, setHook] = useState(location.protocol === 'https:' ? location.origin + '/github/events' : '')
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState('')
  async function submit(event: FormEvent) {
    event.preventDefault()
    setBusy(true)
    setFailure('')
    try {
      const setup = await api<{ action: string; manifest: object }>('/api/github/manifest', {
        method: 'POST',
        body: JSON.stringify({ hook }),
      })
      const form = document.createElement('form')
      form.method = 'POST'
      form.action = setup.action
      const field = document.createElement('input')
      field.type = 'hidden'
      field.name = 'manifest'
      field.value = JSON.stringify(setup.manifest)
      form.append(field)
      document.body.append(form)
      form.submit()
    } catch (e) {
      setFailure(message(e))
      setBusy(false)
    }
  }
  return (
    <form className="integration-setup" onSubmit={submit}>
      <p className="t-caption t-muted">
        Pier needs its own GitHub App. It lives in your GitHub account, so you own it and can remove it whenever you
        like. Here’s everything it’s allowed to do.
      </p>
      <div className="integration-permissions">
        {permissions.map(([label, value]) => (
          <div key={value}>
            <span className="t-caption t-muted">{label}</span>
            <span className="t-caption mono">
              {value}
              <Check size={14} />
            </span>
          </div>
        ))}
      </div>
      <p className="t-note">It can’t change your code, your settings or anyone’s access.</p>
      <label className="field">
        <span className="field-label">Webhook URL</span>
        <input
          type="url"
          className="input mono"
          value={hook}
          onChange={(e) => setHook(e.target.value)}
          placeholder="https://pier.example.com/github/events"
          required
        />
        <span className="t-note">Use the public HTTPS address of your server or tunnel.</span>
      </label>
      {failure && (
        <p className="field-message" role="alert">
          {failure}
        </p>
      )}
      <div className="integration-actions">
        <button className="btn primary" disabled={busy}>
          {busy ? 'Opening GitHub…' : 'Create GitHub App'}
        </button>
        <span className="t-note">
          GitHub opens with the App filled in. Name it, pick the repos, and GitHub sends you back here.
        </span>
      </div>
    </form>
  )
}
