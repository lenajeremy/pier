import { useEffect, useState, type FormEvent } from 'react'
import { api, ApiError, type Build, type RuntimeSettings, type Settings } from '../lib/api'
import { useData } from '../lib/data'
import { Link, navigate } from '../lib/router'
import { BuildFields } from '../components/BuildFields'
import type { Folders } from '../components/FolderPicker'
import { Dialog } from '../components/Dialog'
import { Spinner } from '../components/Spinner'
import { toast } from '../components/Toast'
import { Problem, Skeleton } from '../components/ui'

const empty: Build = { root: '', preset: '', install: '', command: '', output: '' }
const emptyRuntime: RuntimeSettings = { start_command: '', health_path: '', health_off: false, memory_mb: 0, cpus: 0 }
type Draft = { build: Build; runtime: RuntimeSettings; branch: string }

// ProjectSettings is the project's Settings tab: the Project and Build
// panels of D2, the runtime of a server app, and deleting the project.
// Pier detects how to build on every deploy; anything filled in overrides it.
export function ProjectSettings({ project, address, onDeleted }: { project: string; address: string; onDeleted?: () => void }) {
  const { data, error, reload } = useData<Settings>(`/api/projects/${encodeURIComponent(project)}/settings`)
  // The folders of the repo, or of the last upload, for the root directory.
  // A project with nothing to list yet keeps a plain field.
  const tree = useData<Folders>(`/api/projects/${encodeURIComponent(project)}/folders`)
  const [draft, setDraft] = useState<Draft>({ build: empty, runtime: emptyRuntime, branch: '' })
  const [saved, setSaved] = useState<Draft | null>(null)
  const [state, setState] = useState<{ saving?: boolean; error?: string; field?: string }>({})
  const [deleting, setDeleting] = useState(false)

  useEffect(() => {
    if (!data) return
    const d = { build: data.project.build, runtime: { ...emptyRuntime, ...data.project.runtime }, branch: data.project.production_branch }
    setDraft(d)
    setSaved(d)
  }, [data])

  if (error) {
    return (
      <main className="main">
        <Problem>{error.message}</Problem>
      </main>
    )
  }
  if (!data || !saved) {
    return (
      <main className="main">
        <div className="panel">
          <Skeleton rows={4} tall />
        </div>
      </main>
    )
  }

  const b = draft.build
  const rt = draft.runtime
  const dirty = JSON.stringify(draft) !== JSON.stringify(saved)
  const staticOnly = b.preset === 'static' || b.preset === 'vite'
  const defaultMem = data.default_memory_mb ?? 1024
  const change = (d: Partial<Draft>) => {
    setDraft({ ...draft, ...d })
    setState({})
  }
  const setRt = (r: Partial<RuntimeSettings>) => change({ runtime: { ...rt, ...r } })
  const save = async (e: FormEvent) => {
    e.preventDefault()
    setState({ saving: true })
    try {
      await api(`/api/projects/${encodeURIComponent(project)}/settings`, {
        method: 'PUT',
        body: JSON.stringify({ production_branch: draft.branch, build: b, runtime: rt }),
      })
      setSaved(draft)
      setState({})
      reload()
      toast('Saved. The next deployment uses these settings.')
    } catch (err) {
      setState(err instanceof ApiError ? { error: err.message, field: String(err.body.field ?? '') } : { error: "Can't reach pierd." })
    }
  }
  const field = (k: string) => 'field' + (state.field === k ? ' error' : '')
  const help = (k: string, text: string) =>
    state.field === k && state.error ? (
      <span className="field-message" role="alert">
        {state.error}
      </span>
    ) : (
      <span className="t-note">{text}</span>
    )

  return (
    <main className="main">
      <form className="settings" onSubmit={save} noValidate>
        <section className="panel form-panel">
          <h2 className="t-small t-strong">Project</h2>
          <div className="form-grid two">
            <label className="field">
              <span className="field-label">Project name</span>
              <input className="input mono" value={project} readOnly tabIndex={-1} />
              <span className="t-note">
                It lives at {address}. The name is set when the project is made; the address can change under{' '}
                <Link to={`/projects/${encodeURIComponent(project)}/domains`}>Domains</Link>.
              </span>
            </label>
            <label className={field('production_branch')}>
              <span className="field-label">Production branch</span>
              <input
                className="input mono"
                placeholder={data.default_branch}
                value={draft.branch}
                onChange={(e) => change({ branch: e.target.value })}
                spellCheck={false}
                autoCapitalize="off"
              />
              {help(
                'production_branch',
                `Pushes to ${draft.branch || data.default_branch} go to production. Every other branch gets a preview, and so does pier deploy without --prod.`,
              )}
            </label>
          </div>
        </section>

        <section className="panel form-panel">
          <h2 className="t-small t-strong">Build</h2>
          <BuildFields
            build={b}
            onChange={(build) => change({ build })}
            plan={data.last_build_plan}
            problem={state}
            folders={tree.error ? undefined : { data: tree.data, loading: !tree.data }}
            github={!!data.project.repo}
          />
        </section>

        {!staticOnly && (
          <section className="panel form-panel">
            <div className="form-title">
              <h2 className="t-small t-strong">Runtime</h2>
              <span className="t-note">For server apps. A change applies from the next deployment.</span>
            </div>
            <div className="form-grid two">
              <label className={field('start_command')}>
                <span className="field-label">Start command</span>
                <input
                  className="input mono"
                  placeholder="The image's own"
                  value={rt.start_command}
                  onChange={(e) => setRt({ start_command: e.target.value })}
                  spellCheck={false}
                  autoCapitalize="off"
                />
                {help('start_command', 'What the container runs. Leave it empty to use what the image starts with.')}
              </label>
              <div className={field('health_path')}>
                <div className="field-top">
                  <label className="field-label" htmlFor="health-path">
                    Health check path
                  </label>
                  <label className="check">
                    <input type="checkbox" checked={!rt.health_off} onChange={(e) => setRt({ health_off: !e.target.checked })} />
                    <span>Ask for a path</span>
                  </label>
                </div>
                <input
                  id="health-path"
                  className="input mono"
                  placeholder={rt.health_off ? 'Not asked' : '/'}
                  disabled={rt.health_off}
                  value={rt.health_off ? '' : rt.health_path}
                  onChange={(e) => setRt({ health_path: e.target.value })}
                  spellCheck={false}
                  autoCapitalize="off"
                />
                {help(
                  'health_path',
                  rt.health_off
                    ? 'Pier only checks that the process runs, every 30 seconds while production serves it, and restarts it after three failures in a row.'
                    : 'Asked before a deployment goes live, then every 30 seconds while production serves it. Three failures in a row restart it. Empty uses PIER_HEALTH_PATH, or /.',
                )}
              </div>
              <label className={field('memory_mb')}>
                <span className="field-label">Memory limit</span>
                <select className="input" value={String(rt.memory_mb)} onChange={(e) => setRt({ memory_mb: Number(e.target.value) })}>
                  <option value="0">The server's default, {defaultMem >= 1024 ? `${defaultMem / 1024} GB` : `${defaultMem} MB`}</option>
                  <option value="256">256 MB</option>
                  <option value="512">512 MB</option>
                  <option value="1024">1 GB</option>
                  <option value="2048">2 GB</option>
                  <option value="4096">4 GB</option>
                  <option value="8192">8 GB</option>
                  <option value="-1">No limit</option>
                </select>
                {help('memory_mb', "Stops one container from using up the server's memory.")}
              </label>
              <label className={field('cpus')}>
                <span className="field-label">CPU limit</span>
                <select className="input" value={String(rt.cpus)} onChange={(e) => setRt({ cpus: Number(e.target.value) })}>
                  <option value="0">No limit</option>
                  <option value="0.5">Half a CPU</option>
                  <option value="1">1 CPU</option>
                  <option value="2">2 CPUs</option>
                  <option value="4">4 CPUs</option>
                </select>
                {help('cpus', 'The most CPU time the container can take.')}
              </label>
            </div>
          </section>
        )}

        {(dirty || state.error) && (
          <div className="save-bar" role="region" aria-label="Unsaved changes">
            <span className={state.error && !state.field ? 'field-message' : 't-caption'}>
              {state.error && !state.field ? state.error : state.error ? 'One field needs fixing before this can be saved.' : 'You have changes that aren’t saved.'}
            </span>
            <button
              className="btn quiet medium"
              type="button"
              disabled={state.saving}
              onClick={() => {
                setDraft(saved)
                setState({})
              }}
            >
              Discard
            </button>
            <button className="btn primary medium" type="submit" disabled={state.saving || !dirty}>
              {state.saving && <Spinner />}
              {state.saving ? 'Saving…' : 'Save changes'}
            </button>
          </div>
        )}
      </form>

      <section className="panel danger-row">
        <div>
          <h2 className="t-small t-strong">Delete this project</h2>
          <p className="t-caption t-muted">
            Takes {address} offline and removes every deployment, variable, domain and log of {project}. It can't be undone.
          </p>
        </div>
        <button className="btn danger" type="button" onClick={() => setDeleting(true)}>
          Delete project
        </button>
      </section>

      {deleting && (
        <DeleteDialog
          project={project}
          address={address}
          onClose={() => setDeleting(false)}
          onDone={() => {
            onDeleted?.()
            navigate('/')
            toast(`Deleted ${project}`)
          }}
        />
      )}
    </main>
  )
}

// Deleting a project is the one action that asks for its name: it takes a
// live site down and nothing brings it back.
function DeleteDialog({ project, address, onClose, onDone }: { project: string; address: string; onClose: () => void; onDone: () => void }) {
  const [typed, setTyped] = useState('')
  const [busy, setBusy] = useState(false)
  const [problem, setProblem] = useState('')
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (typed.trim() !== project) return
    setBusy(true)
    setProblem('')
    try {
      await api(`/api/projects/${encodeURIComponent(project)}`, { method: 'DELETE' })
      onDone()
    } catch (err) {
      setProblem(err instanceof ApiError ? err.message : "Can't reach pierd.")
      setBusy(false)
    }
  }
  return (
    <Dialog
      title={`Delete ${project}?`}
      busy={busy}
      onClose={onClose}
      onSubmit={submit}
      footer={
        <>
          <button className="btn quiet medium" type="button" disabled={busy} onClick={onClose}>
            Cancel
          </button>
          <button className="btn danger medium" type="submit" disabled={busy || typed.trim() !== project}>
            {busy && <Spinner />}
            {busy ? 'Deleting…' : 'Delete project'}
          </button>
        </>
      }
    >
      <p>
        {address} and every preview go offline at once. Pier stops the project's containers and deletes all of its
        deployments, variables, domains and logs. Nothing brings them back.
      </p>
      <label className={'field' + (problem ? ' error' : '')}>
        <span className="field-label">
          Type <span className="mono">{project}</span> to confirm
        </span>
        <input
          className="input mono"
          value={typed}
          onChange={(e) => {
            setTyped(e.target.value)
            setProblem('')
          }}
          spellCheck={false}
          autoCapitalize="off"
          autoComplete="off"
        />
        {problem && (
          <span className="field-message" role="alert">
            {problem}
          </span>
        )}
      </label>
    </Dialog>
  )
}
