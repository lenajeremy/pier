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
import { NewProject } from './pages/NewProject'
import { Approve } from './pages/Approve'
import { Members } from './pages/Members'
import { ProjectSettings } from './pages/ProjectSettings'
import { Variables } from './pages/Variables'
import { Logs } from './pages/Logs'

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
  '/server': ['Server', 'Disk, memory and CPU, the services Pier runs, and safe clean-up.'],
  '/settings': ["Settings", "Pier's address, the base domain, and your password."],
}

const projectTabs: Record<string, [string, string]> = {
  domains: ['Domains', 'Custom domains, with the DNS records to add and a live check of each one.'],
}

function Dashboard({ session }: { session: Session }) {
  const path = usePath()
  const now = useNow(15_000)
  if (path === '/approve') {
    return <Approve code={new URLSearchParams(location.search).get('code') ?? ''} now={now} />
  }
  return <Signed session={session} path={path} now={now} />
}

function Signed({ session, path, now }: { session: Session; path: string; now: number }) {
  const list = useData<{ projects: ProjectSummary[] }>('/api/projects', () => true, 5000)
  const projects = list.data?.projects ?? []

  const deployment = match('/projects/:name/deployments/:id', path)
  const tab = match('/projects/:name/:tab', path)
  const home = match('/projects/:name', path)
  const name = deployment?.name ?? tab?.name ?? home?.name

  let page: React.ReactNode
  if (name) {
    page = (
      // A deployment's page has its own Visit, for that deployment; one more
      // in the header, going somewhere else, would be a duplicate.
      <ProjectPage name={name} path={path} visit={!deployment}>
        {(project, reload) =>
          deployment ? (
            <DeploymentPage project={name} id={deployment.id} now={now} />
          ) : tab?.tab === 'logs' ? (
            <Logs project={name} address={project.address} />
          ) : tab?.tab === 'variables' ? (
            <Variables project={name} now={now} />
          ) : tab?.tab === 'settings' ? (
            <ProjectSettings project={name} />
          ) : tab && projectTabs[tab.tab] ? (
            <main className="main">
              <Later title={projectTabs[tab.tab][0]} what={projectTabs[tab.tab][1]} />
            </main>
          ) : (
            <ProjectDeployments project={project} session={session} now={now} reload={reload} />
          )
        }
      </ProjectPage>
    )
  } else if (path === '/') {
    page = list.data ? <Projects session={session} projects={projects} now={now} /> : <div className="loading">Loading…</div>
  } else if (path === '/new') {
    page = <NewProject session={session} onCreated={list.reload} />
  } else if (path === '/members') {
    page = <Members now={now} />
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

function ProjectPage({ name, path, visit, children }: {
  name: string
  path: string
  visit: boolean
  children: (p: Project, reload: () => void) => React.ReactNode
}) {
  const { data, error, reload } = useData<Project>(
    `/api/projects/${encodeURIComponent(name)}`,
    () => true,
    3000,
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
        live={visit && !!data?.production}
        path={path}
      />
      {data ? children(data, reload) : <div className="loading">Loading {name}…</div>}
    </>
  )
}
