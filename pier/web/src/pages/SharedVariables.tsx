import { useMemo, useState, type FormEvent } from 'react'
import { api, type ProjectSummary } from '../lib/api'
import { useData } from '../lib/data'
import { plural } from '../lib/format'
import { Pencil, Search, Trash } from '../components/Icons'
import { confirm, Dialog } from '../components/Dialog'
import { Menu } from '../components/Menu'
import { toast } from '../components/Toast'
import { Badge, PageHeading, Paging, Problem, SearchField, Skeleton, usePaged } from '../components/ui'
import { Spinner } from '../components/Spinner'
import { LongValue, ScopePicker, type Scope } from './Variables'
import { Gate, useRole, viewerCant } from '../lib/role'

type Shared = { name: string; projects: string[]; production: boolean; preview: boolean }
const PAGE = 50

// Shared variables is K2: a value set once and linked to the projects that
// need it. A project's own variable with the same name wins.
export function SharedVariables({ projects }: { projects: ProjectSummary[] }) {
  const { data, error, reload } = useData<{ variables: Shared[] }>('/api/shared-variables')
  const [open, setOpen] = useState<Shared | 'new' | null>(null)
  const [query, setQuery] = useState('')
  const { change } = useRole()

  const all = data?.variables ?? []
  const shown = useMemo(() => {
    const q = query.trim().toLowerCase()
    return all.filter((v) => !q || v.name.toLowerCase().includes(q) || v.projects.some((p) => p.includes(q)))
  }, [all, query])
  const paged = usePaged(shown, PAGE, query)

  const remove = (v: Shared) =>
    confirm({
      title: (
        <>
          Delete <span className="mono">{v.name}</span>?
        </>
      ),
      body: (
        <p>
          {v.projects.length === 0
            ? 'No project is linked to it, so nothing changes for any build.'
            : `${listed(v.projects)} ${v.projects.length === 1 ? 'stops' : 'stop'} getting it from the next deployment on. Deployments that are already built keep the value they were built with.`}
        </p>
      ),
      action: 'Delete',
      working: 'Deleting…',
      danger: true,
      run: async () => {
        await api('/api/shared-variables/' + encodeURIComponent(v.name), { method: 'DELETE' })
        reload()
      },
    }).then((ok) => ok && toast(`Deleted ${v.name}`))

  return (
    <main className="main global">
      <PageHeading
        title="Shared variables"
        note="Set a value once and link it to the projects that need it. A project's own variable with the same name wins."
      >
        {all.length > 5 && <SearchField value={query} onChange={setQuery} placeholder="Search shared variables" hotkey="/" width={280} />}
        {all.length > 0 && (
          <Gate allow={change} reason={viewerCant('add shared variables')}>
            <button className="btn primary" onClick={() => setOpen('new')}>
              Add variable
            </button>
          </Gate>
        )}
      </PageHeading>
      {error && <Problem>{error.message}</Problem>}
      {!data && !error && (
        <div className="panel">
          <Skeleton rows={3} />
        </div>
      )}
      {data && all.length === 0 && (
        <div className="empty">
          <span className="t-small t-strong">No shared variables yet</span>
          <span className="t-caption t-muted" style={{ maxWidth: 520 }}>
            A shared variable is one value, like an analytics key or an npm token, that several projects use. Change it here and
            every linked project gets the new value on its next deployment.
          </span>
          <Gate allow={change} reason={viewerCant('add shared variables')}>
            <button className="btn primary" onClick={() => setOpen('new')}>
              Add variable
            </button>
          </Gate>
        </div>
      )}
      {data && all.length > 0 && (
        <div className="stack">
          <div className="table shared">
            <div className="t-head">
              <span>Name</span>
              <span>Value</span>
              <span>Where</span>
              <span>Used by</span>
              <span />
            </div>
            {paged.shown.map((v) => (
              <div className="t-row" key={v.name}>
                <span className="mono" data-tip={v.name.length > 28 ? v.name : undefined}>
                  {v.name}
                </span>
                <span className="mono t-quiet" aria-label="Hidden value" data-tip="A shared value can't be shown again. Replace it instead.">
                  ••••••••••••
                </span>
                <span className="scopes">
                  {v.production && <Badge role="production" />}
                  {v.preview && <Badge role="preview" />}
                </span>
                <span className={v.projects.length ? '' : 't-quiet'} data-tip={v.projects.length > 2 ? v.projects.join(', ') : undefined}>
                  {v.projects.length ? listed(v.projects, 2) : 'No project yet'}
                </span>
                <span className="row-more">
                  {change && (
                    <Menu
                      label={`Actions for ${v.name}`}
                      className="icon-btn bare"
                      items={[
                        { label: 'Edit', icon: <Pencil />, onSelect: () => setOpen(v) },
                        'separator',
                        { label: 'Delete', icon: <Trash />, danger: true, onSelect: () => remove(v) },
                      ]}
                    />
                  )}
                </span>
              </div>
            ))}
            {shown.length === 0 && (
              <div className="t-empty">
                <span className="t-caption t-muted">No shared variable matches “{query.trim()}”.</span>
                <button className="btn quiet small" onClick={() => setQuery('')}>
                  Show all
                </button>
              </div>
            )}
          </div>
          <Paging paged={paged} noun="variable" />
        </div>
      )}
      {open && (
        <SharedDialog
          variable={open === 'new' ? null : open}
          projects={projects}
          onClose={() => setOpen(null)}
          onDone={(name) => {
            const adding = open === 'new'
            setOpen(null)
            reload()
            toast(`${adding ? 'Added' : 'Saved'} ${name}`)
          }}
        />
      )}
    </main>
  )
}

// listed writes names as a sentence would: "blog", "blog and docs",
// "blog, docs and 3 more".
function listed(names: string[], most = 3): string {
  if (names.length <= most) {
    return names.length < 2 ? (names[0] ?? '') : names.slice(0, -1).join(', ') + ' and ' + names[names.length - 1]
  }
  return names.slice(0, most).join(', ') + ' and ' + (names.length - most) + ' more'
}

function SharedDialog({ variable, projects, onClose, onDone }: {
  variable: Shared | null // null to add one
  projects: ProjectSummary[]
  onClose: () => void
  onDone: (name: string) => void
}) {
  const adding = !variable
  const [name, setName] = useState(variable?.name ?? '')
  const [value, setValue] = useState('')
  const [replace, setReplace] = useState(adding)
  const [scopes, setScopes] = useState<Scope[]>(
    variable ? [...(variable.production ? (['production'] as Scope[]) : []), ...(variable.preview ? (['preview'] as Scope[]) : [])] : ['production', 'preview'],
  )
  const [linked, setLinked] = useState<string[]>(variable?.projects ?? [])
  const [find, setFind] = useState('')
  const [problem, setProblem] = useState('')
  const [busy, setBusy] = useState(false)

  const listedProjects = projects.filter((p) => p.name.includes(find.trim().toLowerCase()))
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setProblem('')
    try {
      await api('/api/shared-variables' + (adding ? '' : '/' + encodeURIComponent(name)), {
        method: adding ? 'POST' : 'PUT',
        body: JSON.stringify({ name: name.trim(), projects: linked, value, keep_value: !replace, scopes }),
      })
      onDone(name.trim())
    } catch (err) {
      setProblem(err instanceof Error ? err.message : "Can't reach pierd.")
      setBusy(false)
    }
  }
  return (
    <Dialog
      wide
      title={
        adding ? (
          'Add a shared variable'
        ) : (
          <>
            Edit <span className="mono">{name}</span>
          </>
        )
      }
      busy={busy}
      onClose={onClose}
      onSubmit={submit}
      footer={
        <>
          {problem && (
            <span className="grow field-message" role="alert">
              {problem}
            </span>
          )}
          <button className="btn quiet medium" type="button" disabled={busy} onClick={onClose}>
            Cancel
          </button>
          <button className="btn primary medium" type="submit" disabled={busy || !name.trim()}>
            {busy && <Spinner />}
            {busy ? 'Saving…' : adding ? 'Add variable' : 'Save changes'}
          </button>
        </>
      }
    >
      {adding && (
        <label className="field">
          <span className="field-label">Name</span>
          <input
            className="input mono"
            placeholder="NPM_TOKEN"
            value={name}
            spellCheck={false}
            autoCapitalize="off"
            autoComplete="off"
            onChange={(e) => setName(e.target.value.replace(/\s/g, '_'))}
          />
        </label>
      )}
      <div className="field">
        <div className="field-top">
          <span className="field-label">Value</span>
          {!adding && (
            <label className="check">
              <input type="checkbox" checked={replace} onChange={(e) => setReplace(e.target.checked)} />
              <span>Replace the stored value</span>
            </label>
          )}
        </div>
        {replace ? (
          <>
            <LongValue value={value} onChange={setValue} label="Value" placeholder="Paste the value" autoFocus={!adding} />
            <span className="t-note">Stored encrypted. It can't be shown again after you save it.</span>
          </>
        ) : (
          <span className="t-note">The stored value stays as it is.</span>
        )}
      </div>
      <div className="field">
        <span className="field-label">Where</span>
        <ScopePicker value={scopes} onChange={setScopes} label="Where it applies" />
      </div>
      <div className="field">
        <div className="field-top">
          <span className="field-label">
            Linked projects {linked.length > 0 && <span className="count">{linked.length}</span>}
          </span>
          {projects.length > 1 && (
            <button
              className="btn quiet small"
              type="button"
              onClick={() => setLinked(linked.length === projects.length ? [] : projects.map((p) => p.name))}
            >
              {linked.length === projects.length ? 'Unlink all' : 'Link all'}
            </button>
          )}
        </div>
        {projects.length === 0 ? (
          <span className="t-note">There are no projects yet. Link this once you've made one.</span>
        ) : (
          <div className="pick-list">
            {projects.length > 8 && (
              <label className="pick-find">
                <Search />
                <input placeholder={`Find among ${plural(projects.length, 'project')}`} value={find} onChange={(e) => setFind(e.target.value)} aria-label="Find a project" />
              </label>
            )}
            <div className="pick-rows">
              {listedProjects.map((p) => (
                <label className="check" key={p.name}>
                  <input
                    type="checkbox"
                    checked={linked.includes(p.name)}
                    onChange={(e) => setLinked(e.target.checked ? [...linked, p.name] : linked.filter((n) => n !== p.name))}
                  />
                  <span>{p.name}</span>
                </label>
              ))}
              {listedProjects.length === 0 && <span className="t-note">No project matches.</span>}
            </div>
          </div>
        )}
      </div>
    </Dialog>
  )
}
