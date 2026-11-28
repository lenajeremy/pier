import { useEffect, useRef, useState, type FormEvent } from 'react'
import { api, type Bucket, type BucketVariable, type Environment, type FileListing, type StorageView, type StoredFile } from '../lib/api'
import { failed } from '../lib/actions'
import { useData } from '../lib/data'
import { plural, size } from '../lib/format'
import { navigate } from '../lib/router'
import { Copy, FileIcon, Folder, Pencil, Trash, Upload, Visit } from '../components/Icons'
import { confirm, Dialog } from '../components/Dialog'
import { Menu, type MenuItem } from '../components/Menu'
import { EnvPicker } from '../components/EnvPicker'
import { Sheet } from '../components/Sheet'
import { Spinner } from '../components/Spinner'
import { copy, toast } from '../components/Toast'
import { Badge, Empty, Problem, SearchField, Skeleton, When } from '../components/ui'
import { Gate, useRole, viewerCant } from '../lib/role'

export const storagePath = (project: string) => `/api/projects/${encodeURIComponent(project)}/storage`
const bucketPath = (project: string, name: string) => `${storagePath(project)}/${encodeURIComponent(name)}`

type View = 'buckets' | 'files'

// Storage is the project's Storage tab: its buckets on Cloudflare R2, which
// environments each serves, and the files in them.
export function Storage({ project, now }: { project: string; now: number }) {
  const { data, error, reload } = useData<StorageView>(storagePath(project), () => true, (d) => (d.buckets.some((b) => b.public && !b.address_ready) ? 10_000 : 30_000))
  const [view, setView] = useState<View>('buckets')
  const [chosen, setChosen] = useState<string>()
  const [adding, setAdding] = useState(false)
  const [linking, setLinking] = useState<Bucket>()
  const { change, owner } = useRole()

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
          <Skeleton rows={4} tall />
        </div>
      </main>
    )
  }
  if (!data.cloudflare.connected) {
    return (
      <main className="main">
        <Empty title="Storage needs Cloudflare">
          <span className="t-caption t-muted">Buckets live in your own Cloudflare account.</span>
          {owner ? (
            <button className="btn primary" onClick={() => navigate('/integrations/cloudflare')}>
              Connect Cloudflare
            </button>
          ) : (
            <span className="t-note">The owner connects it in Integrations.</span>
          )}
        </Empty>
      </main>
    )
  }
  const addDialog = adding && (
    <BucketDialog
      project={project}
      buckets={data.buckets}
      domain={data.cloudflare.domain}
      onClose={() => setAdding(false)}
      onDone={(name) => {
        setAdding(false)
        reload()
        toast(`Added the bucket ${name}`)
      }}
    />
  )
  if (data.buckets.length === 0) {
    return (
      <main className="main">
        <Empty title="No buckets yet">
          <span className="t-caption t-muted">Files for {project}, on Cloudflare R2.</span>
          <Gate allow={change} reason={viewerCant('add buckets')}>
            <button className="btn primary" onClick={() => setAdding(true)}>
              Add bucket
            </button>
          </Gate>
        </Empty>
        {addDialog}
      </main>
    )
  }
  const bucket = data.buckets.find((b) => b.name === chosen) ?? data.buckets[0]
  const browse = (b: Bucket) => (setChosen(b.name), setView('files'))
  return (
    <main className="main" style={{ gap: 20 }}>
      <div className="db-views">
        <div className="seg" role="tablist" aria-label="Storage">
          <button role="tab" aria-selected={view === 'buckets'} className={view === 'buckets' ? 'on' : ''} onClick={() => setView('buckets')}>
            Buckets
          </button>
          {change ? (
            <button role="tab" aria-selected={view === 'files'} className={view === 'files' ? 'on' : ''} onClick={() => setView('files')}>
              Files
            </button>
          ) : (
            <Gate allow={false} reason={viewerCant('see the files in buckets')}>
              <button role="tab" aria-selected={false}>
                Files
              </button>
            </Gate>
          )}
        </div>
        {view === 'files' ? (
          <Menu
            label="Choose a bucket"
            align="end"
            className="db-picker"
            items={data.buckets.map((b) => ({ label: b.name, onSelect: () => setChosen(b.name) }))}
          >
            <span className="mono">{bucket.name}</span>
            {bucket.public && <span className="t-note">Public</span>}
          </Menu>
        ) : (
          <Gate allow={change} reason={viewerCant('add buckets')}>
            <button className="btn quiet" onClick={() => setAdding(true)}>
              Add bucket
            </button>
          </Gate>
        )}
      </div>
      {view === 'buckets' ? (
        <Buckets project={project} data={data} reload={reload} onBrowse={browse} onLink={setLinking} />
      ) : (
        <Files key={bucket.name} project={project} bucket={bucket} now={now} onChange={reload} />
      )}
      {addDialog}
      {linking && (
        <LinkDialog
          project={project}
          bucket={linking}
          buckets={data.buckets}
          onClose={() => setLinking(undefined)}
          onDone={() => {
            setLinking(undefined)
            reload()
            toast('Saved. Redeploy to pass it on')
          }}
        />
      )}
    </main>
  )
}

// Buckets lists the project's buckets: what each serves, the variables it
// gives, and how much it holds.
function Buckets({ project, data, reload, onBrowse, onLink }: {
  project: string
  data: StorageView
  reload: () => void
  onBrowse: (b: Bucket) => void
  onLink: (b: Bucket) => void
}) {
  const { change } = useRole()
  const remove = (b: Bucket) =>
    confirm({
      title: `Delete the bucket ${b.name}?`,
      body: <p>{b.files ? `Its ${plural(b.files, 'file')} are deleted, and its key stops working.` : 'Its key stops working.'}</p>,
      action: 'Delete bucket',
      working: 'Emptying and deleting…',
      danger: true,
      run: async () => {
        await api(bucketPath(project, b.name), { method: 'DELETE' })
        reload()
      },
    }).then((ok) => ok && toast(`Deleted the bucket ${b.name}`))
  const copyVars = async (b: Bucket) => {
    try {
      const r = await api<{ variables: BucketVariable[] }>(`${bucketPath(project, b.name)}/variables`)
      await copy(r.variables.map((v) => `${v.name}=${v.value}`).join('\n') + '\n', `${b.name}'s variables`)
    } catch (e) {
      failed(e)
    }
  }
  return (
    <div className="stack">
      <div className="table buckets">
        <div className="t-head">
          <span>Bucket</span>
          <span>Serves</span>
          <span>Variables</span>
          <span className="num">Files</span>
          <span className="num">Size</span>
          <span />
        </div>
        {data.buckets.map((b) => {
          const items: MenuItem[] = [
            { label: 'Browse files', onSelect: () => onBrowse(b) },
            { label: 'Copy variables', icon: <Copy />, onSelect: () => copyVars(b) },
            { label: 'Change', icon: <Pencil />, onSelect: () => onLink(b) },
            'separator',
            { label: 'Delete bucket', icon: <Trash />, danger: true, onSelect: () => remove(b) },
          ]
          return (
            <div
              key={b.name}
              className={'t-row grow' + (change ? ' clickable' : '')}
              onClick={change ? (e) => !(e.target as HTMLElement).closest('button, a, [role="menu"]') && onBrowse(b) : undefined}
            >
              <span className="bucket-name">
                <span className="line">
                  <span className="t-strong mono">{b.name}</span>
                  {b.public && <span className="badge kept">Public</span>}
                </span>
                <span className="t-note mono" data-tip-cut>
                  {b.public_url ? b.public_url.replace('https://', '') : b.bucket}
                </span>
                {b.public && !b.address_ready && <span className="t-note">Setting up its address</span>}
              </span>
              <span className="scopes">
                {b.environments.length ? b.environments.map((e) => <Badge key={e} role={e} />) : <span className="t-muted">Nothing</span>}
              </span>
              <span className="mono t-muted">{b.prefix}_*</span>
              <span className="num t-muted" data-tip={b.count_error}>
                {b.count_error ? '–' : b.more ? `${data.count_up_to.toLocaleString('en-GB')}+` : b.files.toLocaleString('en-GB')}
              </span>
              <span className="num t-muted">{b.count_error ? '–' : b.files ? size(b.bytes) + (b.more ? '+' : '') : '–'}</span>
              <span className="row-more">{change && <Menu label={`Actions for ${b.name}`} items={items} className="icon-btn bare" />}</span>
            </div>
          )
        })}
      </div>
      <span className="t-note">Variables reach a deploy when it starts. Redeploy after a change.</span>
    </div>
  )
}

const prefixOf = (name: string) => {
  const p = name.toUpperCase().replace(/-/g, '_')
  return /^[0-9]/.test(p) ? 'B_' + p : p
}

// varNames says, in one line, which variables a prefix sets.
const varNames = (prefix: string, isPublic: boolean) =>
  `Sets ${prefix || '…'}_BUCKET, ${prefix || '…'}_ACCESS_KEY_ID and ${isPublic ? 4 : 3} more${prefix === 'STORAGE' ? ', and the AWS ones' : ''}.`

// clash finds a bucket that already gives one of these environments the
// same variables.
const clash = (buckets: Bucket[], name: string, prefix: string, envs: Environment[]) =>
  buckets.find((b) => b.name !== name && b.prefix === prefix && b.environments.some((e) => envs.includes(e)))

function BucketDialog({ project, buckets, domain, onClose, onDone }: {
  project: string
  buckets: Bucket[]
  domain?: string
  onClose: () => void
  onDone: (name: string) => void
}) {
  const first = buckets.length === 0
  const [name, setName] = useState(first ? 'files' : '')
  const [isPublic, setPublic] = useState(false)
  const [envs, setEnvs] = useState<Environment[]>(['production', 'preview'])
  const [prefix, setPrefix] = useState<string>()
  const [busy, setBusy] = useState(false)
  const [problem, setProblem] = useState<{ field?: string; message: string }>()
  const auto = first && !clash(buckets, name, 'STORAGE', envs) ? 'STORAGE' : prefixOf(name)
  const shown = prefix ?? auto
  const clashing = clash(buckets, name, shown, envs)
  const taken = buckets.some((b) => b.name === name)
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setProblem(undefined)
    try {
      await api(storagePath(project), { method: 'POST', body: JSON.stringify({ name, public: isPublic, environments: envs, prefix: shown }) })
      onDone(name)
    } catch (err) {
      const body = (err as { body?: { field?: string; error?: string } }).body
      setProblem({ field: body?.field, message: body?.error ?? (err instanceof Error ? err.message : "Can't reach pierd.") })
      setBusy(false)
    }
  }
  const host = domain ? `files-${project}-${name || '…'}.${domain}` : ''
  return (
    <Dialog
      wide
      title="Add a bucket"
      busy={busy}
      onClose={onClose}
      onSubmit={submit}
      footer={
        <>
          {problem && !problem.field && (
            <span className="field-message grow" role="alert">
              {problem.message}
            </span>
          )}
          <button className="btn quiet medium" type="button" disabled={busy} onClick={onClose}>
            Cancel
          </button>
          <button className="btn primary medium" type="submit" disabled={busy || !name || !!clashing || taken}>
            {busy && <Spinner />}
            {busy ? 'Adding…' : 'Add bucket'}
          </button>
        </>
      }
    >
      <label className={'field' + (problem?.field === 'name' || taken ? ' error' : '')}>
        <span className="field-label">Name</span>
        <input
          className="input mono"
          value={name}
          onChange={(e) => (setName(e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, '')), setProblem(undefined))}
          placeholder="uploads"
          maxLength={24}
          spellCheck={false}
          autoCapitalize="off"
        />
        {(problem?.field === 'name' || taken) && (
          <span className="field-message" role="alert">
            {taken ? `There's one called ${name} already.` : problem?.message}
          </span>
        )}
      </label>
      <div className="field">
        <span className="field-label">Access</span>
        <div className="seg" role="radiogroup" aria-label="Access">
          {[false, true].map((p) => (
            <button key={String(p)} type="button" role="radio" aria-checked={isPublic === p} className={isPublic === p ? 'on' : ''} onClick={() => setPublic(p)}>
              {p ? 'Public' : 'Private'}
            </button>
          ))}
        </div>
        <span className="t-note">
          {!isPublic ? 'Only apps with its key read it.' : host ? (
            <>
              Anyone can read its files at <span className="mono">{host}</span>.
            </>
          ) : (
            'Anyone can read its files, at an r2.dev address.'
          )}
        </span>
      </div>
      <div className="field">
        <span className="field-label">Serves</span>
        <EnvPicker value={envs} onChange={setEnvs} />
      </div>
      <label className={'field' + (clashing || problem?.field === 'prefix' ? ' error' : '')}>
        <span className="field-label">Variables</span>
        <input
          className="input mono"
          value={shown}
          onChange={(e) => (setPrefix(e.target.value.toUpperCase().replace(/[^A-Z0-9_]/g, '')), setProblem(undefined))}
          maxLength={31}
          spellCheck={false}
          autoCapitalize="off"
        />
        <span className={clashing || problem?.field === 'prefix' ? 'field-message' : 't-note'} role={clashing ? 'alert' : undefined}>
          {clashing ? `${clashing.name} uses ${shown}_ already.` : problem?.field === 'prefix' ? problem.message : varNames(shown, isPublic)}
        </span>
      </label>
    </Dialog>
  )
}

function LinkDialog({ project, bucket, buckets, onClose, onDone }: {
  project: string
  bucket: Bucket
  buckets: Bucket[]
  onClose: () => void
  onDone: () => void
}) {
  const [envs, setEnvs] = useState<Environment[]>(bucket.environments)
  const [prefix, setPrefix] = useState(bucket.prefix)
  const [busy, setBusy] = useState(false)
  const [problem, setProblem] = useState('')
  const clashing = clash(buckets, bucket.name, prefix, envs)
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setProblem('')
    try {
      await api(bucketPath(project, bucket.name), { method: 'PUT', body: JSON.stringify({ environments: envs, prefix }) })
      onDone()
    } catch (err) {
      setProblem(err instanceof Error ? err.message : "Can't reach pierd.")
      setBusy(false)
    }
  }
  return (
    <Dialog
      wide
      title={
        <>
          Change <span className="mono">{bucket.name}</span>
        </>
      }
      busy={busy}
      onClose={onClose}
      onSubmit={submit}
      footer={
        <>
          {problem && (
            <span className="field-message grow" role="alert">
              {problem}
            </span>
          )}
          <button className="btn quiet medium" type="button" disabled={busy} onClick={onClose}>
            Cancel
          </button>
          <button className="btn primary medium" type="submit" disabled={busy || !prefix || !!clashing}>
            {busy && <Spinner />}
            {busy ? 'Saving…' : 'Save'}
          </button>
        </>
      }
    >
      <div className="field">
        <span className="field-label">Serves</span>
        <EnvPicker value={envs} onChange={setEnvs} />
      </div>
      <label className={'field' + (clashing ? ' error' : '')}>
        <span className="field-label">Variables</span>
        <input
          className="input mono"
          value={prefix}
          onChange={(e) => setPrefix(e.target.value.toUpperCase().replace(/[^A-Z0-9_]/g, ''))}
          maxLength={31}
          spellCheck={false}
          autoCapitalize="off"
        />
        <span className={clashing ? 'field-message' : 't-note'}>{clashing ? `${clashing.name} uses ${prefix}_ already.` : varNames(prefix, bucket.public)}</span>
      </label>
    </Dialog>
  )
}

const isImage = (t: string) => /^image\/(png|jpe?g|gif|webp|avif|svg\+xml)$/.test(t)
const kind = (t: string) => (t ? t.split(';')[0] : '–')
const baseName = (key: string) => key.slice(key.lastIndexOf('/', key.length - 2) + 1)

// Files is a folder of a bucket: its folders, then its files, a page at a
// time. A file opens in a panel; files dropped on the list are uploaded
// into the folder.
function Files({ project, bucket, now, onChange }: { project: string; bucket: Bucket; now: number; onChange: () => void }) {
  const base = bucketPath(project, bucket.name)
  const [folder, setFolder] = useState('')
  const [query, setQuery] = useState('')
  const [typed, setTyped] = useState('')
  const [pages, setPages] = useState<FileListing[]>([])
  const [loading, setLoading] = useState(true)
  const [problem, setProblem] = useState('')
  const [open, setOpen] = useState<number>()
  const [uploading, setUploading] = useState<{ done: number; of: number }>()
  const [dragging, setDragging] = useState(false)
  const picker = useRef<HTMLInputElement>(null)
  const { change } = useRole()

  useEffect(() => {
    const t = setTimeout(() => setQuery(typed), 250)
    return () => clearTimeout(t)
  }, [typed])

  const load = async (after?: string) => {
    setLoading(true)
    try {
      const q = new URLSearchParams({ prefix: folder + query })
      if (after) q.set('after', after)
      const page = await api<FileListing>(`${base}/files?${q}`)
      setPages((p) => (after ? [...p, page] : [page]))
      setProblem('')
    } catch (e) {
      setProblem(e instanceof Error ? e.message : "Can't reach pierd.")
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => {
    setPages([])
    setOpen(undefined)
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [folder, query, base])

  const folders = pages.flatMap((p) => p.folders)
  const files = pages.flatMap((p) => p.files)
  const next = pages.at(-1)?.next
  const crumbs = folder.split('/').filter(Boolean)

  const upload = async (list: FileList | File[]) => {
    const all = Array.from(list)
    if (!all.length) return
    setUploading({ done: 0, of: all.length })
    let done = 0
    for (const f of all) {
      try {
        await api(`${base}/file?key=${encodeURIComponent(folder + f.name)}`, {
          method: 'PUT',
          body: f,
          headers: { 'Content-Type': f.type || 'application/octet-stream' },
        })
        done++
        setUploading({ done, of: all.length })
      } catch (e) {
        failed(e)
        break
      }
    }
    setUploading(undefined)
    if (done) toast(done === 1 ? `Uploaded ${all[0].name}` : `Uploaded ${plural(done, 'file')}`)
    load()
    onChange()
  }
  const removeFolder = (prefix: string) =>
    confirm({
      title: `Delete ${baseName(prefix)}?`,
      body: <p>Every file in it is deleted.</p>,
      action: 'Delete folder',
      working: 'Deleting…',
      danger: true,
      run: async () => {
        await api(`${base}/files?prefix=${encodeURIComponent(prefix)}`, { method: 'DELETE' })
        load()
        onChange()
      },
    }).then((ok) => ok && toast(`Deleted ${baseName(prefix)}`))
  const removeFile = (f: StoredFile) =>
    confirm({
      title: `Delete ${baseName(f.key)}?`,
      body: <p>Apps and links that use it stop finding it.</p>,
      action: 'Delete file',
      working: 'Deleting…',
      danger: true,
      run: async () => {
        await api(`${base}/files?key=${encodeURIComponent(f.key)}`, { method: 'DELETE' })
        setOpen(undefined)
        setPages((p) => p.map((page) => ({ ...page, files: page.files.filter((x) => x.key !== f.key) })))
        onChange()
      },
    }).then((ok) => ok && toast(`Deleted ${baseName(f.key)}`))

  const empty = !loading && !problem && !folders.length && !files.length
  return (
    <section
      className={'table file-list' + (dragging ? ' dropping' : '')}
      onDragOver={(e) => {
        if (!change || !e.dataTransfer.types.includes('Files')) return
        e.preventDefault()
        setDragging(true)
      }}
      onDragLeave={(e) => !e.currentTarget.contains(e.relatedTarget as Node) && setDragging(false)}
      onDrop={(e) => {
        e.preventDefault()
        setDragging(false)
        if (change) upload(e.dataTransfer.files)
      }}
    >
      <div className="files-head">
        <nav className="crumbs mono" aria-label="Folder">
          <button className={crumbs.length ? '' : 'here'} onClick={() => setFolder('')}>
            {bucket.name}
          </button>
          {crumbs.map((c, i) => (
            <span key={i} className="crumb">
              <span className="t-quiet">/</span>
              <button className={i === crumbs.length - 1 ? 'here' : ''} onClick={() => setFolder(crumbs.slice(0, i + 1).join('/') + '/')}>
                {c}
              </button>
            </span>
          ))}
        </nav>
        <div className="card-actions">
          <SearchField value={typed} onChange={setTyped} placeholder="Files starting with…" width={220} />
          <input ref={picker} type="file" multiple hidden onChange={(e) => (upload(e.target.files ?? []), (e.target.value = ''))} />
          <Gate allow={change} reason={viewerCant('upload files')}>
            <button className="btn quiet" disabled={!!uploading} onClick={() => picker.current?.click()}>
              {uploading ? <Spinner /> : <Upload />}
              {uploading ? (uploading.of > 1 ? `Uploading ${uploading.done + 1} of ${uploading.of}…` : 'Uploading…') : 'Upload'}
            </button>
          </Gate>
        </div>
      </div>
      {problem ? (
        <div className="files-empty">
          <Problem>{problem}</Problem>
        </div>
      ) : empty ? (
        <div className="files-empty">
          <span className="t-small t-strong">{query ? `Nothing starts with ${query}` : folder ? 'This folder is empty' : 'No files yet'}</span>
          {!query && !folder && (
            <>
              <div className="var-chips">
                {bucket.variables.map((v) => (
                  <span key={v} className="mono var-chip">
                    {v}
                  </span>
                ))}
              </div>
              <span className="t-note">
                {bucket.environments.length
                  ? `Set on every ${bucket.environments.length === 2 ? 'deploy' : bucket.environments[0] === 'production' ? 'production deploy' : 'preview'}. Any S3 client picks them up.`
                  : "It serves no environment yet. Its variables aren't set anywhere."}
              </span>
            </>
          )}
        </div>
      ) : (
        <>
          <div className="t-head">
            <span>Name</span>
            <span className="num">Size</span>
            <span>Type</span>
            <span>Changed</span>
            <span />
          </div>
          {folders.map((f) => (
            <div key={f} className="t-row clickable" onClick={(e) => !(e.target as HTMLElement).closest('button') && setFolder(f)}>
              <span className="file-name">
                <span className="file-thumb">
                  <Folder />
                </span>
                <span className="mono" data-tip-cut>
                  {baseName(f)}
                </span>
              </span>
              <span />
              <span />
              <span />
              <span className="row-more">
                {change && (
                  <Menu
                    label={`Actions for ${baseName(f)}`}
                    className="icon-btn bare"
                    items={[{ label: 'Delete folder', icon: <Trash />, danger: true, onSelect: () => removeFolder(f) }]}
                  />
                )}
              </span>
            </div>
          ))}
          {files.map((f, i) => (
            <div key={f.key} className="t-row clickable" onClick={(e) => !(e.target as HTMLElement).closest('button') && setOpen(i)}>
              <span className="file-name">
                <span className="file-thumb">{isImage(f.type) ? <img src={f.view} alt="" loading="lazy" /> : <FileIcon />}</span>
                <span className="mono" data-tip-cut>
                  {baseName(f.key)}
                </span>
              </span>
              <span className="num t-muted">{size(f.size)}</span>
              <span className="mono t-muted" data-tip-cut>
                {kind(f.type)}
              </span>
              <span className="t-muted">
                <When at={f.modified} now={now} />
              </span>
              <span className="row-more" />
            </div>
          ))}
        </>
      )}
      {(loading || next) && !problem && (
        <div className="files-more">
          {loading ? (
            <Spinner />
          ) : (
            <button className="btn quiet" onClick={() => load(next)}>
              Show more
            </button>
          )}
        </div>
      )}
      {open !== undefined && files[open] && (
        <FileSheet
          file={files[open]}
          bucket={bucket}
          first={open === 0}
          last={open === files.length - 1}
          onMove={(d) => setOpen((o) => Math.min(files.length - 1, Math.max(0, (o ?? 0) + d)))}
          onClose={() => setOpen(undefined)}
          onDelete={() => removeFile(files[open])}
        />
      )}
    </section>
  )
}

function FileSheet({ file, bucket, first, last, onMove, onClose, onDelete }: {
  file: StoredFile
  bucket: Bucket
  first: boolean
  last: boolean
  onMove: (d: number) => void
  onClose: () => void
  onDelete: () => void
}) {
  const { change } = useRole()
  const t = kind(file.type)
  const name = baseName(file.key)
  const folder = file.key.slice(0, file.key.length - name.length)
  return (
    <Sheet
      label="File"
      onClose={onClose}
      onMove={onMove}
      first={first}
      last={last}
      title={
        <>
          <span className="mono t-note">{folder || bucket.name + '/'}</span>
          <span className="t-small t-strong db-sheet-id mono" data-tip={name.length > 28 ? name : undefined} data-tip-mono>
            {name}
          </span>
        </>
      }
      foot={
        <>
          <a className="btn quiet" href={file.download}>
            Download
          </a>
          <a className="btn quiet visit" href={file.view} target="_blank" rel="noreferrer">
            Open
            <Visit />
          </a>
          {change && (
            <button className="btn quiet danger push" onClick={onDelete}>
              Delete
            </button>
          )}
        </>
      }
    >
      <div className="db-sheet-fields">
        <div className="file-preview" key={file.key}>
          {isImage(t) ? (
            <img src={file.view} alt={name} />
          ) : t.startsWith('video/') ? (
            <video src={file.view} controls preload="metadata" />
          ) : t.startsWith('audio/') ? (
            <audio src={file.view} controls preload="metadata" />
          ) : t === 'application/pdf' ? (
            <iframe src={file.view} title={name} />
          ) : (
            <span className="t-caption t-muted file-nopreview">
              <FileIcon size={28} />
              No preview for {t === '–' ? 'this file' : t}
            </span>
          )}
        </div>
        <div className="db-fields">
          <div className="db-field">
            <span className="t-quiet">Size</span>
            <span>{size(file.size)}</span>
          </div>
          <div className="db-field">
            <span className="t-quiet">Type</span>
            <span className="mono">{t}</span>
          </div>
          <div className="db-field">
            <span className="t-quiet">Changed</span>
            <span>{new Date(file.modified).toLocaleString('en-GB', { dateStyle: 'long', timeStyle: 'short' })}</span>
          </div>
          {file.url && (
            <div className="db-field">
              <span className="t-quiet">Public URL</span>
              <span className="line file-url">
                <span className="mono" data-tip-cut>
                  {file.url.replace('https://', '')}
                </span>
                <button className="icon-btn bare small" aria-label="Copy the public URL" data-tip="Copy" onClick={() => copy(file.url!, 'the public URL')}>
                  <Copy />
                </button>
              </span>
            </div>
          )}
        </div>
      </div>
    </Sheet>
  )
}

