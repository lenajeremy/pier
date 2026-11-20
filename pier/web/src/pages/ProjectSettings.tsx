import { useEffect, useState, type FormEvent } from 'react'
import { api, ApiError, changePreset, type Build, type Settings } from '../lib/api'
import { useData } from '../lib/data'
import { Info } from '../components/Icons'

const empty: Build = { root: '', preset: '', install: '', command: '', output: '' }

// ProjectSettings is the Build panel of D2, on its own tab: Pier detects how
// to build on every deploy, and anything filled in here overrides it.
export function ProjectSettings({ project }: { project: string }) {
  const { data, error } = useData<Settings>(`/api/projects/${encodeURIComponent(project)}/settings`)
  const [b, setB] = useState<Build>(empty)
  const [branch, setBranch] = useState('')
  const [state, setState] = useState<{ saving?: boolean; saved?: boolean; error?: string; field?: string }>({})

  useEffect(() => {
    if (data) {
      setB(data.project.build)
      setBranch(data.project.production_branch)
    }
  }, [data])

  if (error) return <main className="main"><span className="t-note">{error.message}</span></main>
  if (!data) return <div className="loading">Loading settings…</div>

  const server = b.preset === 'dockerfile' || b.preset === 'server'
  const plan = data.last_build_plan
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
        body: JSON.stringify({ production_branch: branch, build: b }),
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
        <div style={{ display: 'flex', alignItems: 'center', gap: 16, justifyContent: 'flex-end' }}>
          {state.error && <span className="field-message">{state.error}</span>}
          {state.saved && <span className="t-note">Saved. The next deployment uses these.</span>}
          <button className="btn primary large" type="submit" disabled={state.saving}>
            {state.saving ? 'Saving…' : 'Save changes'}
          </button>
        </div>
      </form>
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
