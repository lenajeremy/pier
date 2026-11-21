import { useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { ArrowUp, ChevronRight, Folder as FolderIcon } from './Icons'

export type Folder = { path: string; kind: string; app: boolean }
export type Folders = { folders: Folder[]; truncated?: boolean; source?: string }

// What the list shows below the field: the folder being looked in, the
// folders in it that match what's typed, and matches further down the tree.
type Row = { kind: 'up'; path: string } | { kind: 'folder'; folder: Folder; children: number; deep?: boolean }

const parent = (p: string) => (p.includes('/') ? p.slice(0, p.lastIndexOf('/')) : '')
const base = (p: string) => p.slice(p.lastIndexOf('/') + 1)
const clean = (v: string) => v.trim().replace(/^\.\/?/, '').replace(/^\/+/, '')

// FolderPicker is the root directory field with the repo's folders under it.
// The list opens on focus with the top level, so nobody has to know the tree:
// choosing a folder shows what's inside it, typing narrows the folders here
// and finds matches deeper down, and the arrow keys and Enter work as in the
// command menu. Without folders it's a plain text field.
export function FolderPicker({ value, onChange, data, loading, invalid, placeholder = './' }: {
  value: string
  onChange: (path: string) => void
  data?: Folders | null
  loading?: boolean
  invalid?: boolean
  placeholder?: string
}) {
  const [open, setOpen] = useState(false)
  const [active, setActive] = useState(0)
  const input = useRef<HTMLInputElement>(null)
  const list = useRef<HTMLDivElement>(null)
  const [at, setAt] = useState<{ left: number; top: number; width: number; up: boolean } | null>(null)
  const id = useId()

  const tree = useMemo(() => {
    const children = new Map<string, Folder[]>()
    const byPath = new Map<string, Folder>()
    for (const f of data?.folders ?? []) {
      byPath.set(f.path, f)
      if (f.path === '') continue
      const p = parent(f.path)
      children.set(p, [...(children.get(p) ?? []), f])
    }
    return { children, byPath }
  }, [data])

  // Where to look and what to match: "apps/we" looks in apps for "we";
  // "apps/web", a folder with folders in it, looks inside it.
  const v = clean(value)
  const look = v.endsWith('/')
    ? { dir: v.slice(0, -1), q: '' }
    : v && tree.children.has(v)
      ? { dir: v, q: '' }
      : { dir: parent(v), q: base(v).toLowerCase() }

  const rows = useMemo<Row[]>(() => {
    const out: Row[] = []
    if (look.dir) out.push({ kind: 'up', path: parent(look.dir) })
    const here = (tree.children.get(look.dir) ?? []).filter((f) => base(f.path).toLowerCase().includes(look.q))
    here.sort((a, b) => Number(!base(a.path).toLowerCase().startsWith(look.q)) - Number(!base(b.path).toLowerCase().startsWith(look.q)) || a.path.localeCompare(b.path))
    if (!look.dir && !look.q && tree.byPath.has('')) out.push({ kind: 'folder', folder: tree.byPath.get('')!, children: 0 })
    for (const f of here) out.push({ kind: 'folder', folder: f, children: tree.children.get(f.path)?.length ?? 0 })
    // Typing a name finds it anywhere, so "docs" finds apps/docs from the top.
    if (look.q.length >= 2) {
      const shown = new Set(here.map((f) => f.path))
      const deep = (data?.folders ?? [])
        .filter((f) => f.path && !shown.has(f.path) && base(f.path).toLowerCase().includes(look.q))
        .sort((a, b) => Number(b.app) - Number(a.app) || a.path.split('/').length - b.path.split('/').length || a.path.localeCompare(b.path))
        .slice(0, 8)
      for (const f of deep) out.push({ kind: 'folder', folder: f, children: tree.children.get(f.path)?.length ?? 0, deep: true })
    }
    return out
  }, [tree, data, look.dir, look.q])

  const usable = !!data && data.folders.length > 0
  const shown = open && (usable || loading)

  useEffect(() => setActive(rows.findIndex((r) => r.kind === 'folder')), [rows])

  useLayoutEffect(() => {
    if (!shown || !input.current) return
    const place = () => {
      const r = input.current!.getBoundingClientRect()
      const room = window.innerHeight - r.bottom
      setAt({ left: r.left, width: r.width, top: room < 280 && r.top > room ? r.top - 6 : r.bottom + 6, up: room < 280 && r.top > room })
    }
    place()
    window.addEventListener('resize', place)
    window.addEventListener('scroll', place, true)
    return () => {
      window.removeEventListener('resize', place)
      window.removeEventListener('scroll', place, true)
    }
  }, [shown])

  // Keep the highlighted row in sight by scrolling the list alone:
  // scrollIntoView would scroll the page under it too.
  useEffect(() => {
    const l = list.current
    const on = l?.querySelector<HTMLElement>('.on')
    if (!l || !on) return
    if (on.offsetTop < l.scrollTop) l.scrollTop = on.offsetTop - 6
    else if (on.offsetTop + on.offsetHeight > l.scrollTop + l.clientHeight) l.scrollTop = on.offsetTop + on.offsetHeight - l.clientHeight + 6
  }, [active])

  const choose = (row: Row) => {
    if (row.kind === 'up') onChange(row.path)
    else onChange(row.folder.path)
    input.current?.focus()
  }

  const key = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (!shown) {
      if (e.key === 'ArrowDown') {
        e.preventDefault()
        setOpen(true)
      }
      return
    }
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      setActive((i) => Math.min(rows.length - 1, i + 1))
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setActive((i) => Math.max(0, i - 1))
    } else if (e.key === 'Enter') {
      // Enter in the list chooses a folder; it mustn't submit the form.
      e.preventDefault()
      const row = rows[active]
      if (row) choose(row)
      if (!row || (row.kind === 'folder' && row.children === 0)) setOpen(false)
    } else if (e.key === 'ArrowRight' && input.current?.selectionStart === value.length) {
      const row = rows[active]
      if (row?.kind === 'folder' && row.children > 0) {
        e.preventDefault()
        onChange(row.folder.path + '/')
      }
    } else if (e.key === 'Escape') {
      e.preventDefault()
      setOpen(false)
    }
  }

  return (
    <>
      <input
        ref={input}
        className={'input mono' + (invalid ? ' invalid' : '')}
        role="combobox"
        aria-expanded={shown}
        aria-controls={id}
        aria-autocomplete="list"
        aria-activedescendant={shown && rows[active] ? `${id}-${active}` : undefined}
        placeholder={placeholder}
        value={value}
        spellCheck={false}
        autoCapitalize="off"
        autoComplete="off"
        onFocus={() => setOpen(true)}
        onClick={() => setOpen(true)}
        onBlur={(e) => {
          if (!list.current?.contains(e.relatedTarget as Node)) setOpen(false)
        }}
        onChange={(e) => {
          onChange(e.target.value)
          setOpen(true)
        }}
        onKeyDown={key}
      />
      {shown &&
        createPortal(
          <div
            ref={list}
            id={id}
            role="listbox"
            className="folder-list"
            style={at ? { left: at.left, width: at.width, top: at.top, transform: at.up ? 'translateY(-100%)' : undefined } : { visibility: 'hidden' }}
            onMouseDown={(e) => e.preventDefault()}
          >
            {loading && !usable ? (
              <div className="folder-note t-caption t-muted">Reading the repo’s folders…</div>
            ) : (
              <>
                {look.dir && (
                  <div className="folder-where t-caption">
                    <FolderIcon size={14} color="#5E667B" />
                    <span className="mono">{look.dir}/</span>
                  </div>
                )}
                {rows.map((row, i) => {
                  const on = i === active
                  const props = {
                    id: `${id}-${i}`,
                    role: 'option',
                    'aria-selected': on,
                    className: 'folder-row' + (on ? ' on' : ''),
                    onMouseEnter: () => setActive(i),
                    onClick: () => {
                      choose(row)
                      if (row.kind === 'folder' && row.children === 0) setOpen(false)
                    },
                  }
                  if (row.kind === 'up') {
                    return (
                      <div key="up" {...props}>
                        <ArrowUp />
                        <span className="grow">Up to {row.path ? <span className="mono">{row.path}/</span> : 'the top'}</span>
                      </div>
                    )
                  }
                  const f = row.folder
                  const before = rows[i - 1]
                  const firstDeep = row.deep && !(before?.kind === 'folder' && before.deep)
                  return (
                    <div key={f.path || '.'} style={{ display: 'contents' }}>
                      {firstDeep && <div className={'folder-label t-caption' + (i === 0 ? ' top' : '')}>Further down</div>}
                      <div {...props}>
                        <FolderIcon size={16} color={f.app ? '#A3ABBE' : '#5E667B'} />
                        <span className="grow mono">{f.path === '' ? './' : row.deep ? <Path path={f.path} q={look.q} /> : <Name name={base(f.path)} q={look.q} />}</span>
                        {f.path === '' && <span className="folder-kind">The whole repo</span>}
                        {f.path !== '' && f.kind && <span className={'folder-kind' + (f.app ? ' deployable' : '')}>{f.kind}</span>}
                        {row.children > 0 && !row.deep && <ChevronRight />}
                      </div>
                    </div>
                  )
                })}
                {rows.every((r) => r.kind === 'up') && (
                  <div className="folder-note t-caption t-muted">
                    {look.q ? (
                      <>
                        No folder matches “<span className="mono">{look.q}</span>”.
                      </>
                    ) : (
                      'No folders in here.'
                    )}
                  </div>
                )}
                {data?.truncated && <div className="folder-note t-note">The repo is large, so not every folder is listed. Type the path if it isn’t here.</div>}
              </>
            )}
          </div>,
          document.body,
        )}
    </>
  )
}

function Name({ name, q }: { name: string; q: string }) {
  const i = q ? name.toLowerCase().indexOf(q) : -1
  if (i < 0) return <>{name}</>
  return (
    <>
      {name.slice(0, i)}
      <mark>{name.slice(i, i + q.length)}</mark>
      {name.slice(i + q.length)}
    </>
  )
}

function Path({ path, q }: { path: string; q: string }) {
  return (
    <>
      <span className="folder-dim">{parent(path)}/</span>
      <Name name={base(path)} q={q} />
    </>
  )
}
