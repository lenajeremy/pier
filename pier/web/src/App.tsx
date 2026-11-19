import { useCallback, useEffect, useState } from 'react'
import { api, ApiError, type Project, type ProjectSummary, type Session } from './lib/api'
import { useData, useNow } from './lib/data'
import { match, usePath } from './lib/router'
import { Shell } from './components/Shell'
import { Later, ProjectHeader } from './components/ui'
import { SignIn } from './pages/SignIn'
import { Projects } from './pages/Projects'
import { ProjectDeployments } from './pages/ProjectDeployments'
import { DeploymentPage } from './pages/DeploymentPage'

type Auth = { state: 'loading' } | { state: 'out'; address: string } | { state: 'in'; session: Session }

export function App() {
  const [auth, setAuth] = useState<Auth>({ state: 'loading' })

  const check = useCallback(async () => {
    try {
      setAuth({ state: 'in', session: await api<Session>('/api/session') })
    } catch (e) {
      setAuth({ state: 'out', address: e instanceof ApiError && e.status === 401 ? '' : location.host })
    }
  }, [])

  useEffect(() => {
    check()
    const out = () => setAuth({ state: 'out', address: '' })
    window.addEventListener('pier:signed-out', out)
    return () => window.removeEventListener('pier:signed-out', out)
  }, [check])

  if (auth.state === 'loading') return null
  if (auth.state === 'out') return <SignIn address={auth.address} onSignedIn={(session) => setAuth({ state: 'in', session })} />
  return <Dashboard session={auth.session} />
}

const globals: Record<string, [string, string]> = {
  '/activity': ['Activity', 'Deploys, rollbacks, sign-ins and computers approved, across every project, newest first.'],
  '/integrations': ['Integrations', 'Connect GitHub with your own GitHub App, so every push deploys.'],
  '/shared-variables': ['Shared variables', 'Values set once and linked into several projects.'],
  '/members': ['Members', 'Who can sign in, and every browser and computer signed in as you.'],
  '/server': ['Server', 'Disk, memory and CPU, the services Pier runs, and safe clean-up.'],
  '/settings': ["Settings", "Pier's address, the base domain, and your password."],
}

const projectTabs: Record<string, [string, string]> = {
  domains: ['Domains', 'Custom domains, with the DNS records to add and a live check of each one.'],
  variables: ['Variables', 'Environment variables for production and previews, passed to every build.'],
  logs: ['Logs', 'Requests to the site, live, with a chart of requests and errors over time.'],
  settings: ['Settings', 'The build settings Pier detected, and your overrides.'],
}

function Dashboard({ session }: { session: Session }) {
  const path = usePath()
  const now = useNow(15_000)
  const list = useData<{ projects: ProjectSummary[] }>('/api/projects', () => true, 5000)
  const projects = list.data?.projects ?? []

  const deployment = match('/projects/:name/deployments/:id', path)
  const tab = match('/projects/:name/:tab', path)
  const home = match('/projects/:name', path)
  const name = deployment?.name ?? tab?.name ?? home?.name

  let page: React.ReactNode
  if (name) {
    page = (
      <ProjectPage name={name} path={path}>
        {(project) =>
          deployment ? (
            <DeploymentPage project={name} id={deployment.id} session={session} now={now} />
          ) : tab && projectTabs[tab.tab] ? (
            <main className="main">
              <Later title={projectTabs[tab.tab][0]} what={projectTabs[tab.tab][1]} />
            </main>
          ) : (
            <ProjectDeployments project={project} session={session} now={now} />
          )
        }
      </ProjectPage>
    )
  } else if (path === '/') {
    page = list.data ? <Projects session={session} projects={projects} now={now} /> : <div className="loading">Loading…</div>
  } else if (globals[path]) {
    const [title, what] = globals[path]
    page = (
      <main className="main global">
        <div className="page-heading">
          <div>
            <h1 className="t-title" style={{ margin: 0 }}>
              {title}
            </h1>
          </div>
        </div>
        <Later title={title} what={what} />
      </main>
    )
  } else {
    page = (
      <main className="main global">
        <h1 className="t-title" style={{ margin: 0 }}>
          Nothing here
        </h1>
        <span className="t-caption t-muted">There's no page at {path}.</span>
      </main>
    )
  }

  return (
    <Shell session={session} projects={projects} path={path} project={name}>
      {page}
    </Shell>
  )
}

function ProjectPage({ name, path, children }: {
  name: string
  path: string
  children: (p: Project) => React.ReactNode
}) {
  const { data, error } = useData<Project>(
    `/api/projects/${encodeURIComponent(name)}`,
    (p) => p.deployments.some((d) => d.status === 'building'),
    2000,
  )
  if (error) {
    return (
      <main className="main global">
        <h1 className="t-title" style={{ margin: 0 }}>
          {error.status === 404 ? `No project called ${name}` : "Couldn't load this project"}
        </h1>
        <span className="t-caption t-muted">{error.message}</span>
      </main>
    )
  }
  return (
    <>
      <ProjectHeader
        name={name}
        address={data?.address ?? ''}
        url={data?.url ?? ''}
        live={!!data?.production}
        path={path}
      />
      {data ? children(data) : <div className="loading">Loading {name}…</div>}
    </>
  )
}
