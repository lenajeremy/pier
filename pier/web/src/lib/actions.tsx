import { api, type Deployment } from './api'
import { navigate } from './router'
import { confirm } from '../components/Dialog'
import { toast } from '../components/Toast'

// The things that move production, asked and answered the same way wherever
// they start: a button on the project's home, a row's menu, a deployment's page.

const post = <T,>(project: string, path: string, body: object = {}) =>
  api<T>(`/api/projects/${encodeURIComponent(project)}/${path}`, { method: 'POST', body: JSON.stringify(body) })

const Id = ({ children }: { children: string }) => <span className="mono">{children}</span>

export function failed(e: unknown) {
  toast(e instanceof Error ? e.message : "Can't reach pierd.", { tone: 'failed' })
}

type Target = { project: string; host: string; current?: string; branch: string }

// rollBack points production at an older deployment: the one before, or `to`.
export async function rollBack(t: Target, to: string, done: () => void) {
  const ok = await confirm({
    title: `Roll back ${t.project}?`,
    body: (
      <>
        <p>
          {t.host} will serve <Id>{to}</Id> again, in about a third of a second. Nothing is rebuilt
          {t.current ? (
            <>
              , and <Id>{t.current}</Id> stays kept if you want it back.
            </>
          ) : (
            '.'
          )}
        </p>
        <p>Until you promote a deployment or undo this, new pushes to {t.branch} are built but don't go live.</p>
      </>
    ),
    action: 'Roll back',
    working: 'Rolling back…',
    run: async () => {
      await post(t.project, 'rollback', { id: to })
      done()
    },
  })
  if (ok) {
    toast(
      <>
        Rolled back {t.project} to <Id>{to}</Id>
      </>,
      { action: { label: 'Undo', run: () => undoRollBack(t.project, done) } },
    )
  }
}

export async function undoRollBack(project: string, done: () => void) {
  try {
    await post(project, 'rollback/undo')
    done()
    toast(`Undid the rollback of ${project}`)
  } catch (e) {
    failed(e)
  }
}

// promote makes a deployment that already built production's.
export async function promote(t: Target, d: Deployment, done: () => void) {
  const ok = await confirm({
    title: (
      <>
        Promote <Id>{d.id}</Id> to production?
      </>
    ),
    body: (
      <p>
        {t.host} will serve <Id>{d.id}</Id>
        {d.branch ? `, from ${d.branch},` : ''} in about a third of a second. Nothing is rebuilt
        {t.current && t.current !== d.id ? (
          <>
            , and <Id>{t.current}</Id> stays kept if you want it back.
          </>
        ) : (
          '.'
        )}
      </p>
    ),
    action: 'Promote',
    working: 'Promoting…',
    run: async () => {
      await post(t.project, 'promote', { id: d.id })
      done()
    },
  })
  if (ok) {
    toast(
      <>
        {t.host} now serves <Id>{d.id}</Id>
      </>,
    )
  }
}

// redeploy builds a deployment's source again, with today's variables. A
// production build goes live when it works, so that one is asked first.
export async function redeploy(t: Target, d: Deployment) {
  const go = async () => {
    const r = await post<{ id?: string }>(t.project, 'redeploy', { id: d.id })
    if (r.id) navigate(`/projects/${t.project}/deployments/${r.id}`)
  }
  if (d.target !== 'production') {
    try {
      await go()
    } catch (e) {
      failed(e)
    }
    return
  }
  await confirm({
    title: (
      <>
        Redeploy <Id>{d.id}</Id>?
      </>
    ),
    body: (
      <p>
        Pier builds the same source again with the variables as they are now. When the build works, {t.host} serves it.
      </p>
    ),
    action: 'Redeploy',
    working: 'Starting the build…',
    run: go,
  })
}

// restart restarts a deployment's container, or starts it when it was
// stopped or asleep. One started this way stays up while it is visited.
export async function restart(project: string, id: string, done: () => void, stopped = false) {
  try {
    await post(project, `deployments/${encodeURIComponent(id)}/restart`)
    done()
    toast(
      <>
        {stopped ? 'Started' : 'Restarted'} the container for <Id>{id}</Id>
      </>,
    )
  } catch (e) {
    failed(e)
  }
}
