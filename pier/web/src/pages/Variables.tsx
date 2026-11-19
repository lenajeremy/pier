import { useMemo, useState, type FormEvent } from 'react'
import { api, ApiError } from '../lib/api'
import { useData } from '../lib/data'
import { ago } from '../lib/format'
import { navigate } from '../lib/router'
import { Info, More, Search } from '../components/Icons'
import { Badge } from '../components/ui'

type Scope = 'production' | 'preview'
type Entry = { name: string; scopes: Scope[]; updated_at: string }
type Data = { variables: Entry[]; stale: { production: boolean; names: string[]; deployment?: string } }

const where: { label: string; scopes: Scope[] }[] = [
  { label: 'Production', scopes: ['production'] },
  { label: 'Preview', scopes: ['preview'] },
  { label: 'Both', scopes: ['production', 'preview'] },
]

// Variables is G1: add a variable, see every one with where it applies, and
// redeploy when production's build doesn't have the latest values.
export function Variables({ project, now }: { project: string; now: number }) {
  const base = `/api/projects/${encodeURIComponent(project)}`
  const { data, error, reload } = useData<Data>(`${base}/variables`)
  const [name, setName] = useState('')
  const [value, setValue] = useState('')
  const [scopes, setScopes] = useState<Scope[]>(['production', 'preview'])
  const [query, setQuery] = useState('')
  const [problem, setProblem] = useState<{ text: string; field?: string } | null>(null)
  const [busy, setBusy] = useState(false)

  const send = async (path: string, init: RequestInit) => {
    setProblem(null)
    try {
      await api(path, init)
      reload()
      return true
    } catch (e) {
      setProblem(e instanceof ApiError ? { text: e.message, field: String(e.body.field ?? '') } : { text: "Can't reach pierd." })
      return false
    }
  }

  const add = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    if (await send(`${base}/variables`, { method: 'PUT', body: JSON.stringify({ name: name.trim(), value, scopes }) })) {
      setName('')
      setValue('')
    }
    setBusy(false)
  }

  const redeploy = async () => {
    setBusy(true)
    try {
      const r = await api<{ id?: string }>(`${base}/redeploy`, { method: 'POST', body: '{}' })
      if (r.id) navigate(`/projects/${project}/deployments/${r.id}`)
    } catch (e) {
      setProblem({ text: e instanceof ApiError ? e.message : "Can't reach pierd." })
    } finally {
      setBusy(false)
    }
  }

  const shown = useMemo(
    () => (data?.variables ?? []).filter((v) => v.name.toLowerCase().includes(query.trim().toLowerCase())),
    [data, query],
  )
  if (error) return <main className="main"><span className="t-note">{error.message}</span></main>
  if (!data) return <div className="loading">Loading variables…</div>

  const stale = data.stale
  const field = (k: string) => 'field' + (problem?.field === k ? ' error' : '')
  return (
    <main className="main">
      {stale.production && (
        <div className="alert info">
          <Info />
          <div>
            <span className="t-small t-strong">
              {stale.names.length > 0 ? (
                <>
                  Production still uses the old{' '}
                  {stale.names.map((n, i) => (
                    <span key={n}>
                      {i > 0 && (i === stale.names.length - 1 ? ' and ' : ', ')}
                      <span className="mono">{n}</span>
                    </span>
                  ))}
                </>
              ) : (
                'Production was built before a variable was removed'
              )}
            </span>
            <span className="t-caption t-muted">Variables are read when the site builds. Redeploy production to use the new values.</span>
          </div>
          <button className="btn quiet" disabled={busy} onClick={redeploy}>
            Redeploy
          </button>
        </div>
      )}

      <form className="panel form-panel" onSubmit={add} noValidate>
        <span className="t-small t-strong">Add a variable</span>
        <div className="add-fields">
          <label className={field('name')}>
            <span className="field-label">Name</span>
            <input
              className="input mono"
              placeholder="e.g. DATABASE_URL"
              value={name}
              spellCheck={false}
              autoCapitalize="off"
              onChange={(e) => {
                setName(e.target.value.replace(/\s/g, '_'))
                setProblem(null)
              }}
            />
          </label>
          <label className={field('value')}>
            <span className="field-label">Value</span>
            <input
              className="input mono"
              placeholder="Paste a value"
              value={value}
              spellCheck={false}
              autoComplete="off"
              onChange={(e) => {
                setValue(e.target.value)
                setProblem(null)
              }}
            />
          </label>
          <div className="field">
            <span className="field-label">Where</span>
            <div className="seg" role="radiogroup" aria-label="Where it applies">
              {where.map((w) => (
                <button
                  key={w.label}
                  type="button"
                  className={w.scopes.join() === scopes.join() ? 'on' : ''}
                  role="radio"
                  aria-checked={w.scopes.join() === scopes.join()}
                  onClick={() => setScopes(w.scopes)}
                >
                  {w.label}
                </button>
              ))}
            </div>
          </div>
          <button className="btn primary large" type="submit" disabled={busy || !name.trim()}>
            Add
          </button>
        </div>
        {problem && !stale.production ? (
          <span className="field-message" role="alert">
            {problem.text}
          </span>
        ) : (
          <span className="t-note">
            Names that start with VITE_ are built into the site, so the browser can read them. Everything else is only there
            while the site builds.
          </span>
        )}
      </form>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
        <div className="toolbar">
          <div className="title">
            <span className="t-small t-strong">Variables</span>
            <span className="mono t-caption" style={{ color: 'var(--d-note)' }}>
              {data.variables.length}
            </span>
          </div>
          {data.variables.length > 0 && (
            <label className="search-field" style={{ width: 280 }}>
              <Search size={18} />
              <input placeholder="Search variables" value={query} onChange={(e) => setQuery(e.target.value)} aria-label="Search variables" />
            </label>
          )}
        </div>
        <div className="panel">
          {data.variables.length === 0 && (
            <div className="var-row" style={{ gridTemplateColumns: '1fr' }}>
              <span className="t-caption t-muted">No variables yet. Add one above; the next build gets it.</span>
            </div>
          )}
          {shown.map((v) => (
            <Row key={v.name + v.scopes.join()} v={v} base={base} now={now} send={send} />
          ))}
        </div>
        {problem && stale.production && <span className="field-message">{problem.text}</span>}
      </div>
    </main>
  )
}

function Row({ v, base, now, send }: {
  v: Entry
  base: string
  now: number
  send: (path: string, init: RequestInit) => Promise<boolean>
}) {
  const [revealed, setRevealed] = useState<string | null>(null)
  const [editing, setEditing] = useState<string | null>(null)
  const [menu, setMenu] = useState(false)

  const reveal = async () => {
    if (revealed !== null) return setRevealed(null)
    try {
      const r = await api<{ value: string }>(`${base}/variables/reveal`, {
        method: 'POST',
        body: JSON.stringify({ name: v.name, scopes: [v.scopes[0]] }),
      })
      setRevealed(r.value)
    } catch {
      setRevealed(null)
    }
  }
  const save = async () => {
    if (editing === null) return
    if (await send(`${base}/variables`, { method: 'PUT', body: JSON.stringify({ name: v.name, value: editing, scopes: v.scopes }) })) {
      setEditing(null)
      setRevealed(null)
    }
  }

  return (
    <div className="var-row">
      <span className="mono t-caption name">{v.name}</span>
      <span className="value">
        {editing !== null ? (
          <input
            className="input mono"
            autoFocus
            value={editing}
            onChange={(e) => setEditing(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') save()
              if (e.key === 'Escape') setEditing(null)
            }}
            aria-label={`New value for ${v.name}`}
          />
        ) : (
          <>
            <span className={'mono t-caption' + (revealed === null ? ' t-muted' : '')}>{revealed ?? '••••••••••••'}</span>
            <button className="reveal" onClick={reveal} aria-label={revealed === null ? `Show ${v.name}` : `Hide ${v.name}`}>
              <svg viewBox="0 0 24 24" fill="none" stroke="#8A92A8" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" width="16" height="16" aria-hidden>
                <path d="M2.5 12C4.5 7.5 8 5.5 12 5.5S19.5 7.5 21.5 12C19.5 16.5 16 18.5 12 18.5S4.5 16.5 2.5 12Z" />
                <circle cx="12" cy="12" r="3" />
              </svg>
            </button>
          </>
        )}
      </span>
      <span className="scopes">
        {v.scopes.map((s) => (
          <Badge key={s} role={s} />
        ))}
      </span>
      {editing !== null ? (
        <span className="edit-actions">
          <button className="btn primary" onClick={save}>
            Save
          </button>
          <button className="btn quiet" onClick={() => setEditing(null)}>
            Cancel
          </button>
        </span>
      ) : (
        <span className="t-note">Changed {ago(v.updated_at, now).toLowerCase()}</span>
      )}
      <span style={{ position: 'relative' }}>
        {editing === null && (
          <>
            <button className="icon-btn" aria-label={`Actions for ${v.name}`} aria-expanded={menu} onClick={() => setMenu(!menu)}>
              <More />
            </button>
            {menu && (
              <div className="menu" style={{ right: 0, top: 38, minWidth: 180 }}>
                <button
                  className="menu-item"
                  onClick={() => {
                    setMenu(false)
                    setEditing('')
                  }}
                >
                  Change value
                </button>
                <button
                  className="menu-item"
                  onClick={() => {
                    setMenu(false)
                    send(`${base}/variables/delete`, { method: 'POST', body: JSON.stringify({ name: v.name, scopes: v.scopes }) })
                  }}
                >
                  Delete
                </button>
              </div>
            )}
          </>
        )}
      </span>
    </div>
  )
}
