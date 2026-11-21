import { useMemo, useRef, useState, type ChangeEvent, type FormEvent } from 'react'
import { api, ApiError } from '../lib/api'
import { failed } from '../lib/actions'
import { useData } from '../lib/data'
import { plural } from '../lib/format'
import { Link, navigate } from '../lib/router'
import { Copy, Eye, Info, Pencil, Trash, Visit } from '../components/Icons'
import { confirm, Dialog } from '../components/Dialog'
import { Menu } from '../components/Menu'
import { copy, toast } from '../components/Toast'
import { Badge, Paging, Problem, SearchField, Skeleton, usePaged, When } from '../components/ui'

export type Scope = 'production' | 'preview'
type Entry = { name: string; scopes: Scope[]; updated_at: string }
type Data = {
  inherited: Pick<Entry, 'name' | 'scopes'>[]
  variables: Entry[]
  stale: { production: boolean; names: string[]; deployment?: string }
}

const where: { label: string; scopes: Scope[] }[] = [
  { label: 'Production', scopes: ['production'] },
  { label: 'Preview', scopes: ['preview'] },
  { label: 'Both', scopes: ['production', 'preview'] },
]
const PAGE = 50
const MAX = 4096 * 16 // pierd's own limit is what counts; this only draws the counter

// A count of what a pasted .env holds, shown before it's sent. pierd does
// the real parsing.
function countDotEnvEntries(raw: string): number {
  const keys = new Set<string>()
  const text = raw.replace(/\r\n?/g, '\n')
  let i = 0
  while (i < text.length) {
    while (i < text.length && (text[i] === ' ' || text[i] === '\t')) i++
    if (i >= text.length) break
    if (text[i] === '\n') {
      i++
      continue
    }
    if (text[i] === '#') {
      while (i < text.length && text[i] !== '\n') i++
      continue
    }
    if (text.startsWith('export', i) && (text[i + 6] === ' ' || text[i + 6] === '\t')) {
      i += 6
      while (i < text.length && (text[i] === ' ' || text[i] === '\t')) i++
    }
    const start = i
    while (i < text.length && text[i] !== '=' && text[i] !== '\n') i++
    if (text[i] !== '=') {
      while (i < text.length && text[i] !== '\n') i++
      continue
    }
    const key = text.slice(start, i).trim()
    i++
    while (i < text.length && (text[i] === ' ' || text[i] === '\t')) i++
    if (text[i] === '"' || text[i] === "'") {
      const q = text[i++]
      while (i < text.length) {
        if (q === '"' && text[i] === '\\' && i + 1 < text.length) {
          i += 2
          continue
        }
        if (text[i] === q) {
          i++
          break
        }
        i++
      }
      while (i < text.length && text[i] !== '\n') i++
    } else {
      while (i < text.length && text[i] !== '\n') i++
    }
    if (/^[A-Za-z_][A-Za-z0-9_]*$/.test(key)) keys.add(key)
  }
  return keys.size
}

type Open = { kind: 'add'; raw?: string } | { kind: 'import'; raw?: string } | { kind: 'change'; entry: Entry } | null

// Variables is G1: every variable with where it reaches, adding and
// importing them, and a redeploy when production was built with old values.
export function Variables({ project, now }: { project: string; now: number }) {
  const base = `/api/projects/${encodeURIComponent(project)}`
  const { data, error, reload } = useData<Data>(`${base}/variables`)
  const [query, setQuery] = useState('')
  const [open, setOpen] = useState<Open>(null)

  const shown = useMemo(
    () => (data?.variables ?? []).filter((v) => v.name.toLowerCase().includes(query.trim().toLowerCase())),
    [data, query],
  )
  const paged = usePaged(shown, PAGE, query)

  if (error) {
    return (
      <main className="main">
        <Problem>{error.message}</Problem>
      </main>
    )
  }
  if (!data) {
    return (
      <main className="main">
        <div className="panel">
          <Skeleton rows={4} />
        </div>
      </main>
    )
  }

  const stale = data.stale
  const redeploy = () =>
    confirm({
      title: 'Redeploy production?',
      body: (
        <p>
          Pier builds production's source again with the variables as they are now. When the build works, it goes live in
          place of what's serving.
        </p>
      ),
      action: 'Redeploy',
      working: 'Starting the build…',
      run: async () => {
        const r = await api<{ id?: string }>(`${base}/redeploy`, { method: 'POST', body: '{}' })
        if (r.id) navigate(`/projects/${project}/deployments/${r.id}`)
      },
    })

  const remove = (v: Entry) =>
    confirm({
      title: (
        <>
          Delete <span className="mono">{v.name}</span>?
        </>
      ),
      body: (
        <p>
          {v.scopes.length === 2 ? 'Production and preview' : v.scopes[0] === 'production' ? 'Production' : 'Preview'} builds
          stop getting it from the next deployment on. Deployments that are already built keep the value they were built with.
        </p>
      ),
      action: 'Delete',
      working: 'Deleting…',
      danger: true,
      run: async () => {
        await api(`${base}/variables/delete`, { method: 'POST', body: JSON.stringify({ name: v.name, scopes: v.scopes }) })
        reload()
      },
    }).then((ok) => ok && toast(`Deleted ${v.name}`))

  return (
    <main className="main">
      {stale.production && (
        <div className="alert info">
          <Info />
          <div>
            <span className="t-small t-strong">
              {stale.names.length > 0 ? (
                <>
                  Production still uses the old <Names names={stale.names} />
                </>
              ) : (
                'Production was built before its variables changed'
              )}
            </span>
            <span className="t-caption t-muted">Redeploy production so its build and its running process get the new values.</span>
          </div>
          <button className="btn quiet" onClick={redeploy}>
            Redeploy
          </button>
        </div>
      )}

      <div className="stack">
        <div className="toolbar">
          <div className="title">
            <h2 className="t-small t-strong">Variables</h2>
            <span className="count">{data.variables.length.toLocaleString('en-GB')}</span>
          </div>
          {data.variables.length > 5 && <SearchField value={query} onChange={setQuery} placeholder="Search variables" hotkey="/" width={260} />}
          <button className="btn quiet" onClick={() => setOpen({ kind: 'import' })}>
            Import .env
          </button>
          <button className="btn primary" onClick={() => setOpen({ kind: 'add' })}>
            Add variable
          </button>
        </div>

        {data.variables.length === 0 ? (
          <div className="empty">
            <span className="t-small t-strong">No variables yet</span>
            <span className="t-caption t-muted" style={{ maxWidth: 520 }}>
              Variables reach the build, and the running process of a server app. On a static site only names with a public
              prefix, like <span className="mono">VITE_</span>, end up in the browser.
            </span>
            <div className="page-actions">
              <button className="btn quiet" onClick={() => setOpen({ kind: 'import' })}>
                Import .env
              </button>
              <button className="btn primary" onClick={() => setOpen({ kind: 'add' })}>
                Add variable
              </button>
            </div>
          </div>
        ) : (
          <div className="table variables">
            <div className="t-head">
              <span>Name</span>
              <span>Value</span>
              <span>Where</span>
              <span>Changed</span>
              <span />
            </div>
            {paged.shown.map((v) => (
              <Row key={v.name + v.scopes.join()} v={v} base={base} now={now} onChange={() => setOpen({ kind: 'change', entry: v })} onDelete={() => remove(v)} />
            ))}
            {shown.length === 0 && (
              <div className="t-empty">
                <span className="t-caption t-muted">No variable matches “{query.trim()}”.</span>
                <button className="btn quiet small" onClick={() => setQuery('')}>
                  Show all
                </button>
              </div>
            )}
          </div>
        )}
        <Paging paged={paged} noun="variable" />
      </div>

      {data.inherited.length > 0 && (
        <div className="stack">
          <div className="toolbar">
            <div className="title">
              <h2 className="t-small t-strong">Shared with this project</h2>
              <span className="count">{data.inherited.length}</span>
            </div>
            <Link className="btn quiet visit" to="/shared-variables">
              Shared variables
              <Visit />
            </Link>
          </div>
          <div className="table inherited">
            {data.inherited.map((v) => {
              const own = data.variables.some((o) => o.name === v.name && o.scopes.some((s) => v.scopes.includes(s)))
              return (
                <div key={v.name} className="t-row">
                  <span className="mono">{v.name}</span>
                  <span className="scopes">
                    {v.scopes.map((s) => (
                      <Badge key={s} role={s} />
                    ))}
                  </span>
                  <span className="t-note">{own ? `${project}'s own ${v.name} wins where both apply.` : 'Set once, for every project linked to it.'}</span>
                </div>
              )
            })}
          </div>
        </div>
      )}

      {open?.kind === 'add' && (
        <AddDialog
          base={base}
          onClose={() => setOpen(null)}
          onImport={(raw) => setOpen({ kind: 'import', raw })}
          onDone={(name) => {
            setOpen(null)
            reload()
            toast(`Added ${name}`)
          }}
        />
      )}
      {open?.kind === 'import' && (
        <ImportDialog
          base={base}
          raw={open.raw}
          onClose={() => setOpen(null)}
          onDone={(n) => {
            setOpen(null)
            reload()
            toast(`Imported ${plural(n, 'variable')}`)
          }}
        />
      )}
      {open?.kind === 'change' && (
        <ChangeDialog
          base={base}
          entry={open.entry}
          onClose={() => setOpen(null)}
          onDone={() => {
            setOpen(null)
            reload()
            toast(`Changed ${open.entry.name}`)
          }}
        />
      )}
    </main>
  )
}

export function Names({ names }: { names: string[] }) {
  return (
    <>
      {names.map((n, i) => (
        <span key={n}>
          {i > 0 && (i === names.length - 1 ? ' and ' : ', ')}
          <span className="mono">{n}</span>
        </span>
      ))}
    </>
  )
}

function Row({ v, base, now, onChange, onDelete }: {
  v: Entry
  base: string
  now: number
  onChange: () => void
  onDelete: () => void
}) {
  const [revealed, setRevealed] = useState<string | null>(null)
  const read = () =>
    api<{ value: string }>(`${base}/variables/reveal`, {
      method: 'POST',
      body: JSON.stringify({ name: v.name, scopes: [v.scopes[0]] }),
    }).then((r) => r.value)
  const reveal = async () => {
    if (revealed !== null) return setRevealed(null)
    try {
      setRevealed(await read())
    } catch (e) {
      failed(e)
    }
  }
  return (
    <div className="t-row grow">
      <span className="mono name" data-tip={v.name.length > 28 ? v.name : undefined}>
        {v.name}
      </span>
      <span className="value">
        {revealed !== null && revealed.includes('\n') ? (
          <pre className="mono">{revealed}</pre>
        ) : (
          <span className={'mono' + (revealed === null ? ' t-quiet' : '')}>{revealed ?? '••••••••••••'}</span>
        )}
        <button
          className="reveal"
          onClick={reveal}
          aria-label={revealed === null ? `Show ${v.name}` : `Hide ${v.name}`}
          aria-pressed={revealed !== null}
          data-tip={revealed === null ? 'Show the value' : 'Hide the value'}
        >
          <Eye color={revealed === null ? '#8A92A8' : '#ECEEF4'} />
        </button>
      </span>
      <span className="scopes">
        {v.scopes.map((s) => (
          <Badge key={s} role={s} />
        ))}
      </span>
      <When at={v.updated_at} now={now} className="t-muted" />
      <span className="row-more">
        <Menu
          label={`Actions for ${v.name}`}
          className="icon-btn bare"
          items={[
            { label: 'Change value', icon: <Pencil />, onSelect: onChange },
            {
              label: 'Copy value',
              icon: <Copy />,
              onSelect: () =>
                read()
                  .then((value) => copy(value, v.name))
                  .catch(failed),
            },
            'separator',
            { label: 'Delete', icon: <Trash />, danger: true, onSelect: onDelete },
          ]}
        />
      </span>
    </div>
  )
}

// ScopePicker is the segmented control for where a variable reaches.
export function ScopePicker({ value, onChange, label }: { value: Scope[]; onChange: (s: Scope[]) => void; label: string }) {
  return (
    <div className="seg" role="radiogroup" aria-label={label}>
      {where.map((w) => (
        <button
          key={w.label}
          type="button"
          className={w.scopes.join() === value.join() ? 'on' : ''}
          role="radio"
          aria-checked={w.scopes.join() === value.join()}
          onClick={() => onChange(w.scopes)}
        >
          {w.label}
        </button>
      ))}
    </div>
  )
}

// LongValue is the long value field (Components 2): a 12px-corner text area
// in mono that grows with what's in it.
export function LongValue({ value, onChange, placeholder, label, rows = 3, autoFocus }: {
  value: string
  onChange: (v: string) => void
  placeholder?: string
  label: string
  rows?: number
  autoFocus?: boolean
}) {
  return (
    <div className="long-value">
      <textarea
        className="mono"
        rows={Math.min(12, Math.max(rows, value.split('\n').length))}
        placeholder={placeholder}
        value={value}
        autoFocus={autoFocus}
        spellCheck={false}
        autoCapitalize="off"
        autoComplete="off"
        aria-label={label}
        onChange={(e) => onChange(e.target.value)}
      />
      {value.length > 0 && (
        <span className="counter">
          {value.length.toLocaleString('en-GB')}
          {value.length > MAX ? ' characters, which may be more than pierd accepts' : value.length === 1 ? ' character' : ' characters'}
        </span>
      )}
    </div>
  )
}

type Fault = { text: string; field?: string } | null
const fault = (e: unknown): Fault =>
  e instanceof ApiError ? { text: e.message, field: String(e.body.field ?? '') } : { text: "Can't reach pierd." }

function AddDialog({ base, onClose, onDone, onImport }: {
  base: string
  onClose: () => void
  onDone: (name: string) => void
  onImport: (raw: string) => void
}) {
  const [name, setName] = useState('')
  const [value, setValue] = useState('')
  const [scopes, setScopes] = useState<Scope[]>(['production', 'preview'])
  const [problem, setProblem] = useState<Fault>(null)
  const [busy, setBusy] = useState(false)
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setProblem(null)
    try {
      await api(`${base}/variables`, { method: 'PUT', body: JSON.stringify({ name: name.trim(), value, scopes }) })
      onDone(name.trim())
    } catch (err) {
      setProblem(fault(err))
      setBusy(false)
    }
  }
  const bad = (k: string) => problem && (problem.field === k || (!problem.field && k === 'name'))
  return (
    <Dialog
      wide
      title="Add a variable"
      busy={busy}
      onClose={onClose}
      onSubmit={submit}
      footer={
        <>
          <button className="btn quiet medium" type="button" disabled={busy} onClick={onClose}>
            Cancel
          </button>
          <button className="btn primary medium" type="submit" disabled={busy || !name.trim()}>
            {busy ? 'Adding…' : 'Add variable'}
          </button>
        </>
      }
    >
      <label className={'field' + (bad('name') ? ' error' : '')}>
        <span className="field-label">Name</span>
        <input
          className="input mono"
          placeholder="DATABASE_URL"
          value={name}
          spellCheck={false}
          autoCapitalize="off"
          autoComplete="off"
          onPaste={(e) => {
            // A whole .env pasted here belongs in the import; one NAME=value fills both fields.
            const pasted = e.clipboardData.getData('text').trim()
            const one = /^(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)=([^\n]*)$/.exec(pasted)
            if (one) {
              e.preventDefault()
              setName(one[1])
              setValue(one[2].replace(/^(['"])(.*)\1$/, '$2'))
            } else if (pasted.includes('\n') && countDotEnvEntries(pasted) > 1) {
              e.preventDefault()
              onImport(pasted)
            }
          }}
          onChange={(e) => {
            setName(e.target.value.replace(/\s/g, '_'))
            setProblem(null)
          }}
        />
        {bad('name') && (
          <span className="field-message" role="alert">
            {problem!.text}
          </span>
        )}
      </label>
      <div className={'field' + (bad('value') ? ' error' : '')}>
        <span className="field-label">Value</span>
        <LongValue
          value={value}
          label="Value"
          placeholder="Paste the value. Keys and JSON can run over several lines."
          onChange={(v) => {
            setValue(v)
            setProblem(null)
          }}
        />
        {bad('value') ? (
          <span className="field-message" role="alert">
            {problem!.text}
          </span>
        ) : (
          <span className="t-note">Stored encrypted. Builds get it from the next deployment on.</span>
        )}
      </div>
      <div className="field">
        <span className="field-label">Where</span>
        <ScopePicker value={scopes} onChange={setScopes} label="Where it applies" />
      </div>
    </Dialog>
  )
}

function ImportDialog({ base, raw: first, onClose, onDone }: {
  base: string
  raw?: string
  onClose: () => void
  onDone: (count: number) => void
}) {
  const [raw, setRaw] = useState(first ?? '')
  const [scopes, setScopes] = useState<Scope[]>(['production', 'preview'])
  const [problem, setProblem] = useState<Fault>(null)
  const [busy, setBusy] = useState(false)
  const file = useRef<HTMLInputElement>(null)
  const count = useMemo(() => countDotEnvEntries(raw), [raw])
  const pick = async (e: ChangeEvent<HTMLInputElement>) => {
    const f = e.target.files?.[0]
    e.target.value = ''
    if (!f) return
    setRaw(await f.text())
    setProblem(null)
  }
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setProblem(null)
    try {
      await api(`${base}/variables/import`, { method: 'POST', body: JSON.stringify({ raw, scopes }) })
      onDone(count)
    } catch (err) {
      setProblem(fault(err))
      setBusy(false)
    }
  }
  return (
    <Dialog
      wide
      title="Import a .env file"
      busy={busy}
      onClose={onClose}
      onSubmit={submit}
      footer={
        <>
          <span className="grow t-note">{count > 0 ? `${plural(count, 'variable')} found.` : ''}</span>
          <button className="btn quiet medium" type="button" disabled={busy} onClick={onClose}>
            Cancel
          </button>
          <button className="btn primary medium" type="submit" disabled={busy || count === 0}>
            {busy ? 'Importing…' : count > 0 ? `Import ${count}` : 'Import'}
          </button>
        </>
      }
    >
      <div className={'field' + (problem ? ' error' : '')}>
        <div className="field-top">
          <span className="field-label">The file's lines</span>
          <input ref={file} type="file" accept=".env,.txt,text/plain" hidden onChange={pick} />
          <button className="btn quiet small" type="button" onClick={() => file.current?.click()}>
            Choose a file
          </button>
        </div>
        <LongValue
          value={raw}
          rows={7}
          autoFocus
          label="The .env file's lines"
          placeholder={'DATABASE_URL=postgres://…\nAPI_KEY=sk_live_…\nPRIVATE_KEY="-----BEGIN PRIVATE KEY-----\\n…"'}
          onChange={(v) => {
            setRaw(v)
            setProblem(null)
          }}
        />
        {problem ? (
          <span className="field-message" role="alert">
            {problem.text}
          </span>
        ) : (
          <span className="t-note">
            Comments, <span className="mono">export</span> and quoted values over several lines all work. A variable that's
            already here with the same name is replaced.
          </span>
        )}
      </div>
      <div className="field">
        <span className="field-label">Where</span>
        <ScopePicker value={scopes} onChange={setScopes} label="Where the imported variables apply" />
      </div>
    </Dialog>
  )
}

function ChangeDialog({ base, entry, onClose, onDone }: { base: string; entry: Entry; onClose: () => void; onDone: () => void }) {
  const [value, setValue] = useState('')
  const [problem, setProblem] = useState<Fault>(null)
  const [busy, setBusy] = useState(false)
  const [shown, setShown] = useState(false)
  const show = async () => {
    try {
      const r = await api<{ value: string }>(`${base}/variables/reveal`, {
        method: 'POST',
        body: JSON.stringify({ name: entry.name, scopes: [entry.scopes[0]] }),
      })
      setValue(r.value)
      setShown(true)
    } catch (e) {
      setProblem(fault(e))
    }
  }
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setProblem(null)
    try {
      await api(`${base}/variables`, { method: 'PUT', body: JSON.stringify({ name: entry.name, value, scopes: entry.scopes }) })
      onDone()
    } catch (err) {
      setProblem(fault(err))
      setBusy(false)
    }
  }
  return (
    <Dialog
      wide
      title={
        <>
          Change <span className="mono">{entry.name}</span>
        </>
      }
      busy={busy}
      onClose={onClose}
      onSubmit={submit}
      footer={
        <>
          <button className="btn quiet medium" type="button" disabled={busy} onClick={onClose}>
            Cancel
          </button>
          <button className="btn primary medium" type="submit" disabled={busy}>
            {busy ? 'Saving…' : 'Save value'}
          </button>
        </>
      }
    >
      <div className={'field' + (problem ? ' error' : '')}>
        <div className="field-top">
          <span className="field-label">New value</span>
          {!shown && (
            <button className="btn quiet small" type="button" onClick={show}>
              Start from the current value
            </button>
          )}
        </div>
        <LongValue
          value={value}
          autoFocus
          label={`New value for ${entry.name}`}
          placeholder="Paste the new value"
          onChange={(v) => {
            setValue(v)
            setProblem(null)
          }}
        />
        {problem ? (
          <span className="field-message" role="alert">
            {problem.text}
          </span>
        ) : (
          <span className="t-note">
            It reaches {entry.scopes.length === 2 ? 'production and preview' : entry.scopes[0]} builds from the next deployment
            on.
          </span>
        )}
      </div>
    </Dialog>
  )
}
