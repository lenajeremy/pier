import { SharedVariables } from './pages/SharedVariables'
import { Activity } from './pages/Activity'
import { Server } from './pages/Server'
import { Settings } from './pages/Settings'
import { GitHubImport } from './pages/GitHub'
import { Integrations } from './pages/Integrations'
import { useCallback, useEffect, useState } from 'react'
import { api, ApiError, type Project, type ProjectSummary, type Session } from './lib/api'
import { useData, useNow } from './lib/data'
import { Link, match, usePath } from './lib/router'
import { Shell } from './components/Shell'
import { Problem, ProjectHeader, Skeleton } from './components/ui'
import { SignIn } from './pages/SignIn'
import { Projects } from './pages/Projects'
import { ProjectDeployments } from './pages/ProjectDeployments'
import { DeploymentPage } from './pages/DeploymentPage'
import { NewProject } from './pages/NewProject'
import { Approve } from './pages/Approve'
import { Members } from './pages/Members'
import { ProjectSettings } from './pages/ProjectSettings'
import { Variables } from './pages/Variables'
import { Domains } from './pages/Domains'
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

  const importing = match('/new/github/:id', path)
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
            <DeploymentPage
              project={name}
              id={deployment.id}
              now={now}
              host={project.address}
              current={project.production?.deployment?.id}
              branch={project.production_branch || session.server.production_branch}
            />
          ) : tab?.tab === 'logs' ? (
            <Logs project={name} address={project.address} />
          ) : tab?.tab === 'variables' ? (
            <Variables project={name} now={now} />
          ) : tab?.tab === 'settings' ? (
            <ProjectSettings key={name} project={name} address={project.address} onDeleted={list.reload} />
          ) : tab?.tab === 'domains' ? (
            <Domains key={name} project={name} now={now} />
          ) : (
            <ProjectDeployments project={project} session={session} now={now} reload={reload} />
          )
        }
      </ProjectPage>
    )
  } else if (path === '/') {
    page = list.data ? (
      <Projects session={session} projects={projects} now={now} />
    ) : list.error ? (
      <main className="main global">
        <Problem>{list.error.message}</Problem>
      </main>
    ) : (
      <main className="main global">
        <div className="panel">
          <Skeleton rows={4} tall />
        </div>
      </main>
    )
  } else if (path === '/integrations') {
    page = <Integrations />
  } else if (importing) {
    page = <GitHubImport id={importing.id} session={session} onCreated={list.reload} />
  } else if (path === '/new') {
    page = <NewProject session={session} onCreated={list.reload} />
  } else if (path === '/members') {
    page = <Members now={now} />
  } else if (path === '/activity') {
    page = <Activity projects={projects} now={now}/>
  } else if (path === '/shared-variables') {
    page = <SharedVariables projects={projects}/>
  } else if (path === '/server') {
    page = <Server/>
  } else if (path === '/settings') {
    page = <Settings/>
  } else {
    page = (
      <main className="main global">
        <div className="empty">
          <span className="t-small t-strong">There's no page at {path}</span>
          <span className="t-caption t-muted">The address may be mistyped, or the page may have moved.</span>
          <Link className="btn quiet" to="/">
            Go to Projects
          </Link>
        </div>
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
  // The answer carries every deployment of the project, so it's asked for
  // often only while a build or a restart is under way, to show it moving.
  const { data, error, reload } = useData<Project>(
    `/api/projects/${encodeURIComponent(name)}`,
    () => true,
    (p) => (p.deployments.some((d) => d.status === 'building') || p.container?.state === 'restarting' ? 3000 : 15_000),
  )
  // A project that's gone, or pierd being away before anything has loaded.
  // Once it has loaded, a failed refresh keeps what's on screen.
  if (error && (error.status === 404 || !data)) {
    return (
      <main className="main global">
        <div className="empty">
          <span className="t-small t-strong">{error.status === 404 ? `There's no project called ${name}` : `Couldn't load ${name}`}</span>
          <span className="t-caption t-muted">
            {error.status === 404 ? 'It may have been deleted, or the address may be mistyped.' : error.message}
          </span>
          <Link className="btn quiet" to="/">
            Go to Projects
          </Link>
        </div>
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
      {data ? (
        children(data, reload)
      ) : (
        <main className="main">
          <div className="panel">
            <Skeleton rows={2} tall />
          </div>
          <div className="panel">
            <Skeleton rows={5} />
          </div>
        </main>
      )}
    </>
  )
}
