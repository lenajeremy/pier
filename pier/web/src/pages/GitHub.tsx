import { useEffect, useMemo, useState, type FormEvent } from 'react'
import { api, ApiError, type Build, type Session } from '../lib/api'
import { useData, useNow } from '../lib/data'
import { Link, navigate } from '../lib/router'
import { BuildFields } from '../components/BuildFields'
import type { Folder, Folders } from '../components/FolderPicker'
import { ChevronRight, GitHubMark, Visit } from '../components/Icons'
import { Spinner } from '../components/Spinner'
import { Paging, Problem, SearchField, Skeleton, usePaged } from '../components/ui'
import { RepoTable, type Repo as Listed } from './Integrations'
import { Gate, useRole, viewerCant } from '../lib/role'

type Repo = Listed & { default_branch: string; installation: number }
type Catalog = { repositories: Repo[]; installations?: { html_url: string }[] }
const defaults: Build = { root: '', preset: '', install: '', command: '', output: '' }
const PAGE = 10

// GitHubRepos is the Import from GitHub panel of D1: the repos the App can
// read, each one import away from being a project.
export function GitHubRepos() {
  const info = useData<{ slug: string }>('/api/github')
  const { data, error } = useData<Catalog>(info.data?.slug ? '/api/github/repositories' : null)
  const now = useNow()
  const [query, setQuery] = useState('')
  const repos = data?.repositories ?? []
  const shown = useMemo(() => {
    const q = query.trim().toLowerCase()
    return repos.filter((r) => !q || r.full_name.toLowerCase().includes(q))
  }, [repos, query])
  const paged = usePaged(shown, PAGE, query)
  const owners = new Set(repos.map((r) => r.full_name.split('/')[0])).size > 1
  const connected = !!info.data?.slug

  return (
    <div className="stack">
      <div className="toolbar">
        <div className="title">
          <GitHubMark />
          <h2 className="t-small t-strong">Import from GitHub</h2>
          {repos.length > 0 && <span className="count">{repos.length}</span>}
        </div>
        {repos.length > PAGE && <SearchField value={query} onChange={setQuery} placeholder="Search repos" hotkey="/" />}
        {connected && repos.length > 0 && (
          <Link className="btn quiet visit" to="/integrations">
            Choose repos
            <Visit />
          </Link>
        )}
      </div>
      {info.error || error ? (
        <Problem>{(info.error ?? error)!.message}</Problem>
      ) : !info.data || (connected && !data) ? (
        <div className="panel">
          <Skeleton rows={3} />
        </div>
      ) : !connected ? (
        <div className="empty">
          <span className="t-small t-strong">GitHub isn't connected yet</span>
          <span className="t-caption t-muted" style={{ maxWidth: 480 }}>
            Connect it once, pick the repos Pier can read, and every push to them deploys. Pull requests get their own preview
            address.
          </span>
          <Link className="btn primary" to="/integrations">
            Connect GitHub
          </Link>
        </div>
      ) : repos.length === 0 ? (
        <div className="empty">
          <span className="t-small t-strong">Pier can't see any repos yet</span>
          <span className="t-caption t-muted" style={{ maxWidth: 480 }}>
            On GitHub, install the App and choose which repos it can read. They show up here, ready to import.
          </span>
          <Link className="btn primary" to="/integrations">
            Choose repos
          </Link>
        </div>
      ) : (
        <RepoTable repos={paged.shown} owners={owners} now={now} query={shown.length === 0 ? query : ''} onClear={() => setQuery('')} />
      )}
      <Paging paged={paged} noun="repo" />
    </div>
  )
}

// GitHubImport is D2: a repo's name, branch and build, before its first deploy.
export function GitHubImport({ id, session, onCreated }: { id: string; session: Session; onCreated: () => void }) {
  const { data, error } = useData<Catalog>('/api/github/repositories')
  const repo = data?.repositories.find((r) => String(r.id) === id)
  if (error) {
    return (
      <main className="main global">
        <Problem>{error.message}</Problem>
      </main>
    )
  }
  if (!data) {
    return (
      <main className="main global">
        <div className="panel">
          <Skeleton rows={4} tall />
        </div>
      </main>
    )
  }
  if (!repo) {
    return (
      <main className="main global">
        <div className="empty">
          <span className="t-small t-strong">Pier can't see that repo</span>
          <span className="t-caption t-muted" style={{ maxWidth: 480 }}>
            It may have been removed from the App's repos, or renamed. Pick one from the list instead.
          </span>
          <Link className="btn quiet" to="/new">
            Back to New project
          </Link>
        </div>
      </main>
    )
  }
  return <Configure key={id} repo={repo} session={session} onCreated={onCreated} />
}

const slug = (s: string) =>
  s
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .slice(0, 39)
    .replace(/^-|-$/g, '')

// useSettled is a value once it has stopped changing for a moment, so the
// folders are asked for the branch typed, not for every letter of it.
function useSettled<T>(value: T, ms = 400): T {
  const [settled, setSettled] = useState(value)
  useEffect(() => {
    const t = setTimeout(() => setSettled(value), ms)
    return () => clearTimeout(t)
  }, [value, ms])
  return settled
}

// useRoots finds the root directory of each project the repo already deploys,
// so the apps they build can say so.
function useRoots(projects: string[]): Record<string, string> {
  const [roots, setRoots] = useState<Record<string, string>>({})
  const key = projects.join(',')
  useEffect(() => {
    let live = true
    Promise.all(
      projects.map((p) =>
        api<{ project: { build: Build } }>(`/api/projects/${encodeURIComponent(p)}/settings`)
          .then((s) => [s.project.build.root.replace(/^\.?\/+|\/+$/g, ''), p] as const)
          .catch(() => null),
      ),
    ).then((found) => {
      if (live) setRoots(Object.fromEntries(found.filter((f) => f !== null)))
    })
    return () => {
      live = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key])
  return roots
}

function Configure({ repo, session, onCreated }: { repo: Repo; session: Session; onCreated: () => void }) {
  const { change } = useRole()
  const repoName = repo.full_name.split('/')[1]
  const [name, setName] = useState(slug(repoName))
  const [named, setNamed] = useState(false) // the owner typed a name, so choosing an app leaves it
  const [branch, setBranch] = useState(repo.default_branch)
  const [build, setBuild] = useState<Build>(defaults)
  const [problem, setProblem] = useState<{ error?: string; field?: string }>({})
  const [busy, setBusy] = useState(false)
  const domain = session.server.domain
  const settledBranch = useSettled(branch.trim() || repo.default_branch)
  const tree = useData<Folders>(
    `/api/github/repositories/${repo.id}/folders?installation=${repo.installation}&branch=${encodeURIComponent(settledBranch)}`,
  )
  const roots = useRoots(repo.projects ?? [])
  const apps = (tree.data?.folders ?? []).filter((f) => f.app)
  const root = build.root.trim().replace(/^\.?\/+|\/+$/g, '')

  // Choosing an app makes it the root, and names the project after it while
  // the owner hasn't named it themselves.
  const pick = (f: Folder | null) => {
    setBuild({ ...build, root: f?.path ?? '' })
    if (!named) {
      const leaf = f?.path.split('/').pop() ?? ''
      setName(slug(!leaf || leaf === repoName ? repoName : `${repoName}-${leaf}`))
    }
    setProblem({})
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setProblem({})
    try {
      await api('/api/github/import', {
        method: 'POST',
        body: JSON.stringify({ name, branch, build, repo: repo.id, installation: repo.installation }),
      })
      onCreated()
      navigate('/projects/' + name)
    } catch (err) {
      setProblem(err instanceof ApiError ? { error: err.message, field: String(err.body.field ?? '') } : { error: "Can't reach pierd." })
      setBusy(false)
    }
  }
  const bad = (k: string) => problem.field === k
  return (
    <main className="main global">
      <nav className="breadcrumb" aria-label="Breadcrumb">
        <Link to="/">Projects</Link>
        <ChevronRight />
        <Link to="/new">New project</Link>
        <ChevronRight />
        <span className="mono" style={{ color: 'var(--d-text)' }}>
          {name || repo.full_name.split('/')[1]}
        </span>
      </nav>
      <header className="page-heading">
        <div>
          <h1 className="t-title">Configure {name || 'the project'}</h1>
          <p className="t-caption t-muted repo-line">
            <GitHubMark size={15} />
            <span className="mono">{repo.full_name}</span>
          </p>
        </div>
      </header>
      <form className="settings" onSubmit={submit} noValidate>
        <section className="panel form-panel">
          <h2 className="t-small t-strong">Project</h2>
          {apps.length >= 2 && (
            <div className="field">
              <span className="field-label">App</span>
              <div className="app-choices" role="radiogroup" aria-label="The app this project builds">
                {[null, ...apps.slice(0, 11)].map((f) => {
                  const path = f?.path ?? ''
                  const used = roots[path]
                  return (
                    <label key={path || '.'} className="check app-choice">
                      <input type="radio" name="app" checked={root === path} onChange={() => pick(f)} />
                      <span className="mono app-path">{path || './'}</span>
                      <span className="t-note">{used ? `${used} deploys it` : f ? f.kind : 'The whole repo'}</span>
                    </label>
                  )
                })}
              </div>
              <span className="t-note">
                This repo holds {apps.length} apps. Each can be its own project with its own address: pick the one this project
                builds{apps.length > 11 ? ', or find another under Root directory' : ''}.
              </span>
            </div>
          )}
          <div className="form-grid two">
            <label className={'field' + (bad('name') ? ' error' : '')}>
              <span className="field-label">Project name</span>
              <input
                className="input mono"
                value={name}
                spellCheck={false}
                autoCapitalize="off"
                onChange={(e) => {
                  setName(e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, '-'))
                  setNamed(true)
                  setProblem({})
                }}
              />
              {bad('name') ? (
                <span className="field-message" role="alert">
                  {problem.error}
                </span>
              ) : (
                <span className="t-note">
                  It lives at {name || 'my-site'}.{domain}.
                </span>
              )}
            </label>
            <label className={'field' + (bad('branch') ? ' error' : '')}>
              <span className="field-label">Production branch</span>
              <input
                className="input mono"
                value={branch}
                spellCheck={false}
                autoCapitalize="off"
                onChange={(e) => {
                  setBranch(e.target.value)
                  setProblem({})
                }}
              />
              {bad('branch') ? (
                <span className="field-message" role="alert">
                  {problem.error}
                </span>
              ) : (
                <span className="t-note">
                  Pushes to {branch || repo.default_branch} go to production. Every other branch and pull request gets a preview.
                </span>
              )}
            </label>
          </div>
        </section>
        <section className="panel form-panel">
          <h2 className="t-small t-strong">Build</h2>
          <BuildFields
            build={build}
            onChange={setBuild}
            problem={problem}
            folders={tree.error ? undefined : { data: tree.data, loading: !tree.data }}
            github
          />
        </section>
        {problem.error && !problem.field && <Problem>{problem.error}</Problem>}
        <div className="form-foot">
          <Gate allow={change} reason={viewerCant('import repositories')}>
            <button disabled={busy || !name || !branch} className="btn primary large">
              {busy && <Spinner />}
              {busy ? 'Starting the first build…' : `Deploy ${name || 'the project'}`}
            </button>
          </Gate>
          <span className="t-note">Pier builds {branch || repo.default_branch} now, and again on every push. Variables can be added once it exists.</span>
        </div>
      </form>
    </main>
  )
}
