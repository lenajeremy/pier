import { useEffect, useState, type FormEvent } from 'react'
import { api, ApiError, changePreset, type Build, type RuntimeSettings, type Settings } from '../lib/api'
import { useData } from '../lib/data'
import { navigate } from '../lib/router'
import { Info } from '../components/Icons'

const empty: Build = { root: '', preset: '', install: '', command: '', output: '' }
const emptyRuntime: RuntimeSettings = { start_command: '', health_path: '', memory_mb: 0, cpus: 0 }

// ProjectSettings is the Build panel of D2, on its own tab: Pier detects how
// to build on every deploy, and anything filled in here overrides it.
export function ProjectSettings({ project, onDeleted }: { project: string; onDeleted?: () => void }) {
  const { data, error } = useData<Settings>(`/api/projects/${encodeURIComponent(project)}/settings`)
  const [b, setB] = useState<Build>(empty)
  const [rt, setRt] = useState<RuntimeSettings>(emptyRuntime)
  const [branch, setBranch] = useState('')
  const [state, setState] = useState<{ saving?: boolean; saved?: boolean; error?: string; field?: string }>({})
  const [confirmDelete, setConfirmDelete] = useState('')
  const [deleting, setDeleting] = useState(false)
  const [deleteError, setDeleteError] = useState('')

  useEffect(() => {
    if (data) {
      setB(data.project.build)
      setRt(data.project.runtime ?? emptyRuntime)
      setBranch(data.project.production_branch)
    }
  }, [data])

  if (error) return <main className="main"><span className="t-note">{error.message}</span></main>
  if (!data) return <div className="loading">Loading settings…</div>

  const server = b.preset === 'dockerfile' || b.preset === 'server'
  const staticOnly = b.preset === 'static' || b.preset === 'vite'
  const plan = data.last_build_plan
  const defaultMem = data.default_memory_mb ?? 1024
  const set = (k: keyof Build) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => {
    setB(k === 'preset' ? changePreset(b, e.target.value as Build['preset']) : { ...b, [k]: e.target.value })
    setState({})
  }
  const save = async (e: FormEvent) => {
    e.preventDefault()
    setState({ saving: true })
    try {
      await api(`/api/projects/${encodeURIComponent(project)}/settings`, {
        method: 'PUT',
        body: JSON.stringify({ production_branch: branch, build: b, runtime: rt }),
      })
      setState({ saved: true })
    } catch (err) {
      setState(err instanceof ApiError ? { error: err.message, field: String(err.body.field ?? '') } : { error: "Can't reach pierd." })
    }
  }
  const field = (k: string) => 'field' + (state.field === k ? ' error' : '')

  return (
    <main className="main">
      <form onSubmit={save} style={{ display: 'flex', flexDirection: 'column', gap: 20 }} noValidate>
        <section className="panel form-panel">
          <span className="t-small t-strong">Build</span>
          <div className="alert info">
            <Info />
            <div>
              {plan ? (
                <>
                  <span className="t-small t-strong">The last build used {plan.label}</span>
                  <span className="t-caption t-muted">
                    {steps(plan)} Pier works this out again on every deploy. Fill in a field to override it.
                  </span>
                </>
              ) : (
                <>
                  <span className="t-small t-strong">Pier works out how to build on every deploy</span>
                  <span className="t-caption t-muted">
                    Pier checks the project root for a Dockerfile, a Node application, or static files.
                  </span>
                </>
              )}
            </div>
          </div>
          <div className="form-grid">
            <label className={field('preset')}>
              <span className="field-label">Preset</span>
              <select className="input" value={b.preset} onChange={set('preset')}>
                <option value="">Detect it</option>
                <option value="static">Static HTML</option>
                <option value="vite">Vite</option>
                <option value="server">Railpack web server</option>
                <option value="dockerfile">Dockerfile</option>
              </select>
              <span className="t-note">Choose Railpack web server for a server-rendered app.</span>
            </label>
            <label className={field('root')}>
              <span className="field-label">Root directory</span>
              <input className="input mono" placeholder="./" value={b.root} onChange={set('root')} spellCheck={false} />
              <span className="t-note">The directory containing the app or Dockerfile.</span>
            </label>
            <label className={field('output')}>
              <span className="field-label">Output folder</span>
              <input className="input mono" disabled={server} placeholder={plan?.output || 'dist'} value={b.output} onChange={set('output')} spellCheck={false} />
              <span className="t-note">Applies to static sites.</span>
            </label>
            <label className={field('install')}>
              <span className="field-label">Install command</span>
              <input className="input mono" disabled={server} placeholder={plan ? plan.install || 'none' : 'npm ci'} value={b.install} onChange={set('install')} spellCheck={false} />
              <span className="t-note">Picked from the lockfile.</span>
            </label>
            <label className={field('command')}>
              <span className="field-label">Build command</span>
              <input className="input mono" disabled={b.preset === 'dockerfile'} placeholder={plan ? plan.build || 'none' : 'npm run build'} value={b.command} onChange={set('command')} spellCheck={false} />
              <span className="t-note">The build script in package.json.</span>
            </label>
            <label className={field('production_branch')}>
              <span className="field-label">Production branch</span>
              <input
                className="input mono"
                placeholder={data.default_branch}
                value={branch}
                onChange={(e) => {
                  setBranch(e.target.value)
                  setState({})
                }}
                spellCheck={false}
              />
              <span className="t-note">For git pushes. pier deploy goes to production with --prod.</span>
            </label>
          </div>
        </section>
        {!staticOnly && (
          <section className="panel form-panel">
            <span className="t-small t-strong">Runtime</span>
            <div className="form-grid">
              <label className={field('start_command')}>
                <span className="field-label">Start command</span>
                <input
                  className="input mono"
                  placeholder="Image default"
                  value={rt.start_command}
                  onChange={(e) => {
                    setRt({ ...rt, start_command: e.target.value })
                    setState({})
                  }}
                  spellCheck={false}
                />
                <span className="t-note">Overrides the container start command on the next server deploy.</span>
              </label>
              <label className={field('health_path')}>
                <span className="field-label">Healthcheck path</span>
                <input
                  className="input mono"
                  placeholder="/"
                  value={rt.health_path}
                  onChange={(e) => {
                    setRt({ ...rt, health_path: e.target.value })
                    setState({})
                  }}
                  spellCheck={false}
                />
                <span className="t-note">HTTP path checked before traffic switches to a new container.</span>
              </label>
              <label className={field('memory_mb')}>
                <span className="field-label">Memory limit</span>
                <select
                  className="input"
                  value={String(rt.memory_mb)}
                  onChange={(e) => {
                    setRt({ ...rt, memory_mb: Number(e.target.value) })
                    setState({})
                  }}
                >
                  <option value="0">Default ({defaultMem >= 1024 ? `${defaultMem / 1024} GB` : `${defaultMem} MB`})</option>
                  <option value="256">256 MB</option>
                  <option value="512">512 MB</option>
                  <option value="1024">1 GB</option>
                  <option value="2048">2 GB</option>
                  <option value="4096">4 GB</option>
                  <option value="8192">8 GB</option>
                  <option value="-1">Unlimited</option>
                </select>
                <span className="t-note">Prevents one container from exhausting the server's memory.</span>
              </label>
              <label className={field('cpus')}>
                <span className="field-label">CPU limit</span>
                <select
                  className="input"
                  value={String(rt.cpus)}
                  onChange={(e) => {
                    setRt({ ...rt, cpus: Number(e.target.value) })
                    setState({})
                  }}
                >
                  <option value="0">Unlimited</option>
                  <option value="0.5">0.5 vCPU</option>
                  <option value="1">1 vCPU</option>
                  <option value="2">2 vCPU</option>
                  <option value="4">4 vCPU</option>
                </select>
                <span className="t-note">Applies to server containers on the next deploy.</span>
              </label>
            </div>
          </section>
        )}
        <div style={{ display: 'flex', alignItems: 'center', gap: 16, justifyContent: 'flex-end' }}>
          {state.error && <span className="field-message">{state.error}</span>}
          {state.saved && <span className="t-note">Saved. The next deployment uses these.</span>}
          <button className="btn primary large" type="submit" disabled={state.saving}>
            {state.saving ? 'Saving…' : 'Save changes'}
          </button>
        </div>
      </form>

      <section className="panel form-panel" style={{ marginTop: 12 }}>
        <span className="t-small t-strong">Delete project</span>
        <span className="t-caption t-muted" style={{ maxWidth: 640 }}>
          Permanently removes <span className="mono">{project}</span>, stops its running containers, removes its routes and custom domains, and deletes all deployments, variables, and saved build logs.
        </span>
        <div style={{ display: 'flex', alignItems: 'flex-end', gap: 12, flexWrap: 'wrap' }}>
          <label className="field" style={{ flex: '1 1 260px', maxWidth: 360 }}>
            <span className="field-label">Type {project} to confirm</span>
            <input
              className="input mono"
              placeholder={project}
              value={confirmDelete}
              onChange={(e) => {
                setConfirmDelete(e.target.value)
                setDeleteError('')
              }}
              spellCheck={false}
            />
          </label>
          <button
            className="btn danger large"
            type="button"
            disabled={deleting || confirmDelete.trim() !== project}
            onClick={async () => {
              setDeleting(true)
              setDeleteError('')
              try {
                await api(`/api/projects/${encodeURIComponent(project)}`, { method: 'DELETE' })
                onDeleted?.()
                navigate('/')
              } catch (err) {
                setDeleteError(err instanceof ApiError ? err.message : "Can't reach pierd.")
                setDeleting(false)
              }
            }}
          >
            {deleting ? 'Deleting…' : 'Delete project'}
          </button>
        </div>
        {deleteError && (
          <span className="field-message" role="alert">
            {deleteError}
          </span>
        )}
      </section>
    </main>
  )
}

function steps(plan: Record<string, string>): string {
  if (plan.runtime) return 'The image supplies the start command.'
  const ran = [plan.install, plan.build].filter(Boolean)
  const where = plan.root && plan.root !== '.' ? ` in ${plan.root}/` : ''
  const serve = `served ${plan.output === '.' ? 'the folder itself' : plan.output + '/'}`
  if (ran.length === 0) return `It ran nothing${where} and ${serve}.`
  return `It ran ${ran.join(', then ')}${where}, and ${serve}.`
}
