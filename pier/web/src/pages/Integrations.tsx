import { useMemo, useState, type FormEvent } from 'react'
import { api } from '../lib/api'
import { failed } from '../lib/actions'
import { useData, useNow } from '../lib/data'
import { plural } from '../lib/format'
import { Link } from '../lib/router'
import { Check, Docs, GitHubMark, Trash, Visit, Warning } from '../components/Icons'
import { confirm } from '../components/Dialog'
import { Menu } from '../components/Menu'
import { toast } from '../components/Toast'
import { PageHeading, Paging, Problem, SearchField, Skeleton, usePaged, When } from '../components/ui'

// A job waits for the owner once per commit: a monorepo's projects that the
// same push would build come as one, named in projects.
type Job = { id: number; project: string; projects?: string[]; pr: number; sha: string; state: string; detail: string }
const who = (j: Job) => names(j.projects?.length ? j.projects : [j.project])
const names = (list: string[]) => (list.length < 2 ? list[0] : `${list.slice(0, -1).join(', ')} and ${list[list.length - 1]}`)
type Info = { slug: string; hook: string; jobs: Job[] }
type Installation = { id: number; account: { login: string; type: string }; html_url: string }
export type Repo = { id: number; full_name: string; private: boolean; language: string; pushed_at: string; projects?: string[] }
type Catalog = { repositories: Repo[]; installations: Installation[] }
type Delivery = { delivered_at: string; event: string; status_code: number; duration: number }
type Activity = { created_at: string; deliveries: Delivery[] }

const permissions = [
  ['Read the code in the repos you pick', 'contents: read'],
  ['Hear about pushes and pull requests', 'push, pull_request events'],
  ['Post the preview link on pull requests', 'pull requests: write'],
  ["Show each build's status on its commit", 'commit statuses: write'],
]
const message = (err: unknown) => (err instanceof Error ? err.message : "Can't reach pierd.")
const PAGE = 20

// Integrations is C1 to C3: connecting GitHub with the owner's own App, what
// the connection looks like once it works, and builds waiting on the owner.
export function Integrations() {
  const { data, error, reload } = useData<Info>('/api/github', () => true, 10000)
  const catalog = useData<Catalog>(data?.slug ? '/api/github/repositories' : null)
  const activity = useData<Activity>(data?.slug ? '/api/github/activity' : null, () => true, 60000)
  const now = useNow()
  const [query, setQuery] = useState('')

  const installs = catalog.data?.installations ?? []
  const installed = installs.length > 0
  const repos = catalog.data?.repositories ?? []
  const shown = useMemo(() => {
    const q = query.trim().toLowerCase()
    return repos.filter((r) => !q || r.full_name.toLowerCase().includes(q) || (r.language ?? '').toLowerCase().includes(q))
  }, [repos, query])
  const paged = usePaged(shown, PAGE, query)

  const managed = data?.slug ? 'https://github.com/settings/apps/' + data.slug : ''
  const installURL = 'https://github.com/apps/' + data?.slug + '/installations/new'
  const last = activity.data?.deliveries[0]
  const hook = data?.hook ? new URL(data.hook) : null
  const status = !data?.slug
    ? 'Not connected'
    : !catalog.data
      ? catalog.error
        ? "Can't reach GitHub"
        : 'Checking the installation…'
      : installed
        ? 'Connected'
        : 'Waiting for you to install it'

  // disconnect forgets the App, so a new one can be made: after its secret
  // was changed on GitHub, or the App was deleted there.
  const disconnect = async () => {
    const ok = await confirm({
      title: 'Disconnect GitHub?',
      body: (
        <>
          <p>
            Pier forgets the App <span className="mono">{data?.slug}</span>. Pushes stop deploying, and builds waiting to start
            are dismissed. Projects keep their repos and every deployment, and they deploy again once you connect a new App.
          </p>
          <p>The App itself stays in your GitHub account. Delete it there too if you won't use it again.</p>
        </>
      ),
      action: 'Disconnect',
      working: 'Disconnecting…',
      danger: true,
      run: async () => {
        await api('/api/github', { method: 'DELETE' })
        await reload()
      },
    })
    if (ok) toast('Disconnected GitHub')
  }
  const dismiss = async (j: Job) => {
    try {
      await api('/api/github/jobs/' + j.id + '/dismiss', { method: 'POST' })
      await reload()
      toast(`Dismissed ${j.sha.slice(0, 7)}`)
    } catch (e) {
      failed(e)
    }
  }

  const resume = async (j: Job) => {
    const go = async () => {
      await api('/api/github/jobs/' + j.id + '/resume', { method: 'POST' })
      await reload()
    }
    if (j.state !== 'approval') {
      try {
        await go()
        toast(`Trying ${j.sha.slice(0, 7)} again`)
      } catch (e) {
        failed(e)
      }
      return
    }
    const ok = await confirm({
      title: 'Build this commit from a fork?',
      body: (
        <>
          <p>
            <span className="mono">{j.sha.slice(0, 7)}</span> comes from someone else's copy of the repo. Building it runs their
            code on this server, with {(j.projects?.length ?? 1) > 1 ? `the preview variables of ${who(j)}` : `${j.project}'s preview variables`}.
          </p>
          <p>Read the changes on GitHub first. Approving covers this commit only; a later push asks again.</p>
        </>
      ),
      action: 'Approve and build',
      working: 'Starting the build…',
      run: go,
    })
    if (ok) toast(`Building ${j.sha.slice(0, 7)} for ${who(j)}`)
  }

  return (
    <main className="main global">
      <PageHeading title="Integrations" note="The outside services Pier works with." />
      {error && <Problem>{error.message}</Problem>}
      {!data && !error && (
        <div className="panel">
          <Skeleton rows={3} tall />
        </div>
      )}
      {data && (
        <section className="panel" aria-label="GitHub">
          <header className="integration-head">
            <span className="integration-mark">
              <GitHubMark size={22} />
            </span>
            <div className="integration-identity">
              <h2 className="t-small t-strong">GitHub</h2>
              <p className="t-caption t-muted">Import repos, and deploy every push and pull request.</p>
            </div>
            <span className="integration-state t-caption">
              <i className={'swatch ' + (installed ? 'ready' : data.slug && catalog.error ? 'failed' : '')} />
              {status}
            </span>
            {data.slug && (
              <a className="btn quiet visit" href={catalog.data && !installed ? installURL : managed} target="_blank" rel="noreferrer">
                {catalog.data && !installed ? 'Install on GitHub' : 'Manage on GitHub'}
                <Visit />
              </a>
            )}
            {data.slug && (
              <Menu
                label="More for GitHub"
                items={[{ label: 'Disconnect', icon: <Trash />, danger: true, onSelect: disconnect }]}
              />
            )}
          </header>
          {data.slug && hook ? (
            <div className="strip" style={{ ['--cells' as string]: 4 }}>
              <div>
                <span className="t-note">App</span>
                <span className="t-small t-strong mono-value">{data.slug}</span>
                <span className="t-note">
                  {activity.data?.created_at
                    ? 'Made ' + new Date(activity.data.created_at).toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric' })
                    : 'In your GitHub account'}
                </span>
              </div>
              <div>
                <span className="t-note">Installed on</span>
                <span className="t-small t-strong">
                  {installs.map((i) => i.account.login).join(', ') || (catalog.data ? 'Nothing yet' : 'Checking…')}
                </span>
                <span className="t-note">
                  {installed
                    ? `${installs.length === 1 ? (installs[0].account.type === 'Organization' ? 'An organisation' : 'Your account') : plural(installs.length, 'account')}, with ${plural(repos.length, 'repo')}`
                    : 'Install the App to pick repos'}
                </span>
              </div>
              <div>
                <span className="t-note">Events go to</span>
                <div className="figure">
                  <span className="t-small t-strong mono-value">{hook.pathname}</span>
                  {!!activity.data?.deliveries.length && <Durations deliveries={activity.data.deliveries} />}
                </div>
                <span className="t-note">On {hook.host}</span>
              </div>
              <div>
                <span className="t-note">Last event</span>
                <span className="t-small t-strong integration-state">
                  {last && <i className={'swatch ' + (last.status_code >= 200 && last.status_code < 300 ? 'ready' : 'failed')} />}
                  {last ? <When at={last.delivered_at} now={now} /> : activity.error ? "Can't tell" : activity.data ? 'None lately' : 'Checking…'}
                </span>
                <span className="t-note">
                  {last
                    ? `A ${last.event.replaceAll('_', ' ')}, ${last.status_code ? 'answered ' + last.status_code : 'which never arrived'}`
                    : (activity.error?.message ?? 'GitHub tells Pier about each push')}
                </span>
              </div>
            </div>
          ) : (
            <Connect />
          )}
        </section>
      )}

      {!!data?.jobs.length && (
        <div className="stack">
          <div className="toolbar">
            <div className="title">
              <h2 className="t-small t-strong">Waiting for you</h2>
              <span className="count">{data.jobs.length}</span>
            </div>
          </div>
          <div className="panel">
            {data.jobs.map((j) => (
              <div className="attention-row" key={j.id}>
                <Warning size={20} color={j.state === 'approval' ? '#F0B85C' : '#FFB4B4'} />
                <div>
                  <span className="t-small">
                    {(j.projects?.length ? j.projects : [j.project]).map((p, i, all) => (
                      <span key={p}>
                        {i > 0 && (i === all.length - 1 ? ' and ' : ', ')}
                        <Link className="t-strong" to={'/projects/' + p}>
                          {p}
                        </Link>
                      </span>
                    ))}
                    {j.pr > 0 && `, pull request #${j.pr}`} <span className="mono id">{j.sha.slice(0, 7)}</span>
                  </span>
                  <span className="t-note">
                    {j.state === 'approval'
                      ? `This commit comes from a fork. It isn't built until you approve it, because building runs its code with ${(j.projects?.length ?? 1) > 1 ? "these projects'" : "this project's"} preview variables.`
                      : j.detail}
                  </span>
                </div>
                <button className="btn quiet" onClick={() => dismiss(j)} data-tip={j.state === 'approval' ? "Don't build this commit" : 'Leave it failed'}>
                  Dismiss
                </button>
                <button className="btn quiet" onClick={() => resume(j)}>
                  {j.state === 'approval' ? 'Review and approve' : 'Try again'}
                </button>
              </div>
            ))}
          </div>
        </div>
      )}

      {data?.slug && (
        <div className="stack">
          <div className="toolbar">
            <div className="title">
              <h2 className="t-small t-strong">Repos Pier can see</h2>
              {repos.length > 0 && <span className="count">{repos.length}</span>}
            </div>
            {repos.length > 8 && <SearchField value={query} onChange={setQuery} placeholder="Search repos" hotkey="/" />}
            {installed && (
              <a className="btn quiet visit" href={installs.length === 1 ? installs[0].html_url : installURL} target="_blank" rel="noreferrer">
                Choose repos on GitHub
                <Visit />
              </a>
            )}
          </div>
          {catalog.error ? (
            <Problem>{catalog.error.message}</Problem>
          ) : !catalog.data ? (
            <div className="panel">
              <Skeleton rows={3} />
            </div>
          ) : repos.length === 0 ? (
            <div className="empty">
              <span className="t-small t-strong">{installed ? 'No repos picked yet' : "The App isn't installed yet"}</span>
              <span className="t-caption t-muted" style={{ maxWidth: 480 }}>
                {installed
                  ? 'On GitHub, choose which repos the App can read. They show up here, ready to import.'
                  : "Install the App on your account or an organisation, and pick the repos it can read. Pier can't see any others."}
              </span>
              <a className="btn primary" href={installed ? installs[0].html_url : installURL} target="_blank" rel="noreferrer">
                {installed ? 'Choose repos on GitHub' : 'Install on GitHub'}
              </a>
            </div>
          ) : (
            <RepoTable repos={paged.shown} owners={installs.length > 1} now={now} query={shown.length === 0 ? query : ''} onClear={() => setQuery('')} />
          )}
          <Paging paged={paged} noun="repo" />
        </div>
      )}
    </main>
  )
}

// RepoTable lists repos with what to do with each: import it, or open the
// project it already is.
export function RepoTable({ repos, owners, now, query, onClear }: {
  repos: Repo[]
  owners: boolean // show whose repo each is
  now: number
  query?: string // a search that found nothing
  onClear?: () => void
}) {
  return (
    <div className="table repos">
      <div className="t-head">
        <span>Repo</span>
        <span>Language</span>
        <span>Last push</span>
        <span />
      </div>
      {repos.map((r) => (
        <div className="t-row" key={r.id}>
          <span className="repo-name">
            <Docs size={16} color="#8A92A8" />
            <span className="t-strong" data-tip={owners ? undefined : r.full_name}>
              {owners ? r.full_name : r.full_name.split('/').pop()}
            </span>
            <span className="t-note">{r.private ? 'Private' : 'Public'}</span>
            {r.projects?.length ? (
              <span className="t-note repo-projects">
                Deploys{' '}
                {r.projects.length > 3 ? (
                  <span data-tip={r.projects.join(', ')}>{r.projects.length} projects</span>
                ) : (
                  r.projects.map((p, i) => (
                    <span key={p}>
                      {i > 0 && (i === r.projects!.length - 1 ? ' and ' : ', ')}
                      <Link to={'/projects/' + p}>{p}</Link>
                    </span>
                  ))
                )}
              </span>
            ) : null}
          </span>
          <span className="t-muted">{r.language || ''}</span>
          {r.pushed_at ? <When at={r.pushed_at} now={now} className="t-muted" /> : <span />}
          <span className="row-end">
            {/* One repo can deploy several projects, a monorepo's apps each as its own. */}
            <Link className={'btn ' + (r.projects?.length ? 'quiet' : 'primary')} to={'/new/github/' + r.id}>
              {r.projects?.length ? 'Add project' : 'Import'}
            </Link>
          </span>
        </div>
      ))}
      {query && (
        <div className="t-empty">
          <span className="t-caption t-muted">No repo matches “{query.trim()}”.</span>
          <button className="btn quiet small" onClick={onClear}>
            Show all
          </button>
        </div>
      )}
    </div>
  )
}

function Durations({ deliveries }: { deliveries: Delivery[] }) {
  const recent = [...deliveries].reverse()
  const max = Math.max(...recent.map((d) => d.duration), 0.001)
  return (
    <span className="bars" role="img" aria-label="How long pierd took to answer GitHub's recent events">
      {recent.map((d, i) => (
        <i
          key={i}
          className={d.status_code >= 400 ? 'failed' : i === recent.length - 1 ? 'newest' : ''}
          data-tip={`${d.event.replaceAll('_', ' ')}, answered in ${Math.round(d.duration * 1000)} ms`}
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
    <form className="integration-setup" onSubmit={submit} noValidate>
      <p className="t-caption t-muted">
        Pier needs its own GitHub App. It lives in your GitHub account, so you own it and can remove it whenever you like. Here's
        everything it's allowed to do.
      </p>
      <div className="integration-permissions">
        {permissions.map(([label, value]) => (
          <div key={value}>
            <span className="t-caption">{label}</span>
            <span className="t-caption mono t-muted">
              {value}
              <Check size={14} />
            </span>
          </div>
        ))}
      </div>
      <p className="t-note">It can't change your code, your settings or anyone's access.</p>
      <label className={'field' + (failure ? ' error' : '')}>
        <span className="field-label">Where GitHub sends events</span>
        <input
          type="url"
          className="input mono"
          value={hook}
          onChange={(e) => {
            setHook(e.target.value.trim())
            setFailure('')
          }}
          placeholder="https://pier.example.com/github/events"
          spellCheck={false}
          autoCapitalize="off"
        />
        {failure ? (
          <span className="field-message" role="alert">
            {failure}
          </span>
        ) : (
          <span className="t-note">This server's public HTTPS address, or a tunnel to it. GitHub has to be able to reach it.</span>
        )}
      </label>
      <div className="form-foot">
        <span className="t-note">GitHub opens with the App filled in. Name it, pick the repos, and GitHub sends you back here.</span>
        <button className="btn primary" disabled={busy || !hook}>
          {busy ? 'Opening GitHub…' : 'Create the GitHub App'}
        </button>
      </div>
    </form>
  )
}
