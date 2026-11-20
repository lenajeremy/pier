import { useState, type FormEvent } from 'react'
import { api, type ProjectSummary } from '../lib/api'
import { useData } from '../lib/data'
import { Plus } from '../components/Icons'

type Shared = { name: string; projects: string[]; production: boolean; preview: boolean }
const empty = (): Shared => ({ name: '', projects: [], production: true, preview: true })
export function SharedVariables({ projects }: { projects: ProjectSummary[] }) {
  const { data, error, reload } = useData<{ variables: Shared[] }>('/api/shared-variables')
  const [draft, edit] = useState<Shared | null>(null),
    [creating, setCreating] = useState(false),
    [value, setValue] = useState(''),
    [replace, setReplace] = useState(false),
    [problem, setProblem] = useState(''),
    [busy, setBusy] = useState(false)
  function open(v?: Shared) {
    edit(v ?? empty())
    setCreating(!v)
    setValue('')
    setReplace(!v)
    setProblem('')
  }
  function close() {
    edit(null)
    setValue('')
    setProblem('')
  }
  async function save(e: FormEvent) {
    e.preventDefault()
    if (!draft) return
    if (!draft.production && !draft.preview) {
      setProblem('Choose at least one scope.')
      return
    }
    setBusy(true)
    setProblem('')
    try {
      await api('/api/shared-variables' + (creating ? '' : '/' + encodeURIComponent(draft.name)), {
        method: creating ? 'POST' : 'PUT',
        body: JSON.stringify({
          name: draft.name,
          projects: draft.projects,
          value,
          keep_value: !replace,
          scopes: [...(draft.production ? ['production'] : []), ...(draft.preview ? ['preview'] : [])],
        }),
      })
      close()
      await reload()
    } catch (e) {
      setProblem((e as Error).message)
    } finally {
      setBusy(false)
    }
  }
  async function remove() {
    if (!draft || !window.confirm('Remove ' + draft.name + ' from all linked projects?')) return
    setBusy(true)
    try {
      await api('/api/shared-variables/' + encodeURIComponent(draft.name), { method: 'DELETE' })
      close()
      await reload()
    } catch (e) {
      setProblem((e as Error).message)
    } finally {
      setBusy(false)
    }
  }
  return (
    <main className="main global operations">
      <header className="page-heading">
        <div>
          <h1 className="t-title">Shared variables</h1>
          <p className="t-caption t-muted">
            Set a value once and link it to projects. A project’s own variable with the same name wins.
          </p>
        </div>
        <button className="btn primary" onClick={() => open()}>
          <Plus size={14} />
          Add variable
        </button>
      </header>
      {error && <p role="alert">{error.message}</p>}
      {draft && (
        <form className="panel ops-panel" onSubmit={save}>
          <h2>{creating ? 'Add variable' : 'Edit ' + draft.name}</h2>
          <label className="field">
            <span>Name</span>
            <input
              autoFocus
              className="input mono"
              required
              disabled={!creating}
              value={draft.name}
              onChange={(e) => edit({ ...draft, name: e.target.value })}
            />
          </label>
          {!creating && (
            <label className="ops-choice">
              <input type="checkbox" checked={replace} onChange={(e) => setReplace(e.target.checked)} />
              Replace the stored value
            </label>
          )}
          {replace && (
            <label className="field">
              <span>Value</span>
              <input
                className="input mono"
                type="password"
                autoComplete="new-password"
                value={value}
                onChange={(e) => setValue(e.target.value)}
              />
            </label>
          )}
          <fieldset>
            <legend>Where</legend>
            {(['production', 'preview'] as const).map((s) => (
              <label className="ops-choice" key={s}>
                <input type="checkbox" checked={draft[s]} onChange={(e) => edit({ ...draft, [s]: e.target.checked })} />
                {s === 'production' ? 'Production' : 'Preview'}
              </label>
            ))}
          </fieldset>
          <fieldset>
            <legend>Linked projects</legend>
            {!projects.length && <p className="t-note">Create a project to link it here.</p>}
            {projects.map((p) => (
              <label className="ops-choice" key={p.name}>
                <input
                  type="checkbox"
                  checked={draft.projects.includes(p.name)}
                  onChange={(e) =>
                    edit({
                      ...draft,
                      projects: e.target.checked
                        ? [...draft.projects, p.name]
                        : draft.projects.filter((n) => n !== p.name),
                    })
                  }
                />
                {p.name}
              </label>
            ))}
          </fieldset>
          <div className="ops-actions">
            <button className="btn primary" disabled={busy}>
              Save variable
            </button>
            <button className="btn quiet" type="button" onClick={close} disabled={busy}>
              Cancel
            </button>
            {!creating && (
              <button className="btn quiet" type="button" onClick={remove} disabled={busy}>
                Delete
              </button>
            )}
          </div>
          {problem && (
            <p className="field-message" role="alert">
              {problem}
            </p>
          )}
        </form>
      )}
      {data && (
        <div className="panel ops-table">
          <table>
            <thead>
              <tr>
                {['Name', 'Value', 'Where', 'Used by', ''].map((h, i) => (
                  <th key={i}>{h}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {data.variables.map((v) => (
                <tr key={v.name}>
                  <td>
                    <strong className="mono">{v.name}</strong>
                    {v.name.startsWith('VITE_') && <small>Built into the site, so the browser can read it.</small>}
                  </td>
                  <td className="mono" aria-label="Hidden value">
                    ••••••••••••
                  </td>
                  <td>{[v.production && 'Production', v.preview && 'Preview'].filter(Boolean).join(', ')}</td>
                  <td>{v.projects.join(', ') || 'No projects'}</td>
                  <td>
                    <button className="btn quiet" onClick={() => open(v)} aria-label={'Edit ' + v.name}>
                      Edit
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {data.variables.length === 0 && <p className="ops-empty">No shared variables yet.</p>}
        </div>
      )}
    </main>
  )
}
