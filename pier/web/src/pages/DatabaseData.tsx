import { useEffect, useMemo, useState, type FormEvent } from 'react'
import { type DbPage, type DbTable } from '../lib/api'
import { useData } from '../lib/data'
import { pretty, Value, isNumeric, json } from '../lib/sql'
import { ChevronLeft, ChevronRight, Copy } from '../components/Icons'
import { Sheet } from '../components/Sheet'
import { copy } from '../components/Toast'
import { Problem, SearchField, Skeleton } from '../components/ui'
import { databasePath } from './Database'

const PAGE = 50

const newTable = `CREATE TABLE items (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  name text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);`

// alignRight says whether a column's values line up on the right: amounts
// do, while keys and the ids that point at other rows read as names.
const alignRight = (p: DbPage, c: { name: string; type: string }) => isNumeric(c.type) && !p.key?.includes(c.name) && !p.refs[c.name]

// singular names one row of a table: agents to agent.
const singular = (table: string) => (table.length > 3 && table.endsWith('s') && !table.endsWith('ss') ? table.slice(0, -1) : table)

const count = (n: number, more: boolean) => (more ? `${n.toLocaleString('en-GB')}+` : n.toLocaleString('en-GB'))

// DataView is the Database tab's Data: the tables on the left, and the
// chosen one's rows, a page at a time, with a filter and a sort.
export function DataView({ project, db, onQuery }: { project: string; db: string; onQuery: (draft?: string) => void }) {
  const side = useSideWidth()
  const tables = useData<{ tables: DbTable[] }>(`${databasePath(project)}/tables?db=${encodeURIComponent(db)}`, () => false, 30_000)
  const [chosen, setChosen] = useState<{ schema: string; name: string } | null>(null)
  const [search, setSearch] = useState('')
  const list = tables.data?.tables ?? []
  useEffect(() => setChosen(null), [db])
  const current = chosen ?? (list[0] ? { schema: list[0].schema, name: list[0].name } : null)

  if (tables.error) return <Problem>{tables.error.message}</Problem>
  if (!tables.data) {
    return (
      <div className="panel">
        <Skeleton rows={6} />
      </div>
    )
  }
  if (list.length === 0) return <NoTables onQuery={onQuery} />

  const q = search.trim().toLowerCase()
  const shown = list.filter((t) => !q || t.name.toLowerCase().includes(q))
  const schemas = [...new Set(shown.map((t) => t.schema))]
  return (
    <div className="db-browser" style={{ ['--db-side' as string]: `${side.width}px` }}>
      <SideHandle side={side} />
      <div className="panel db-tables">
        {list.length > 8 && (
          <div className="db-tables-search">
            <SearchField value={search} onChange={setSearch} placeholder={`Search ${list.length} tables`} width="100%" />
          </div>
        )}
        <div className="db-tables-list">
          {schemas.map((schema) => (
            <div key={schema} className="db-schema">
              {(schemas.length > 1 || schema !== 'public') && <span className="db-schema-name mono">{schema}</span>}
              {shown
                .filter((t) => t.schema === schema)
                .map((t) => (
                  <button
                    key={t.name}
                    className={'db-table' + (current?.schema === t.schema && current?.name === t.name ? ' on' : '')}
                    onClick={() => setChosen({ schema: t.schema, name: t.name })}
                  >
                    <span className="mono" data-tip-cut data-tip-mono>
                      {t.name}
                    </span>
                    {t.kind === 'table' ? <span className="db-count">{count(t.rows, t.more)}</span> : <span className="db-kind">View</span>}
                  </button>
                ))}
            </div>
          ))}
          {shown.length === 0 && <span className="t-note db-none">No table matches “{search.trim()}”.</span>}
        </div>
      </div>
      {current && <Rows key={`${db}/${current.schema}/${current.name}`} project={project} db={db} schema={current.schema} name={current.name} onOpen={setChosen} onQuery={onQuery} />}
    </div>
  )
}

function NoTables({ onQuery }: { onQuery: (draft?: string) => void }) {
  return (
    <div className="panel db-empty">
      <div className="db-empty-text">
        <span className="t-small t-strong">No tables yet</span>
        <span className="t-caption t-muted">They show up once your app's migrations run.</span>
      </div>
      <div className="db-commands">
        <div className="db-command">
          <span className="t-quiet">$</span>
          <span className="grow">pier db tunnel</span>
          <span className="t-note">from your computer</span>
        </div>
        <div className="db-command">
          <span className="t-quiet">$</span>
          <span className="grow">npx prisma migrate deploy</span>
          <span className="t-note">then migrate</span>
        </div>
      </div>
      <button className="btn quiet" onClick={() => onQuery(newTable)}>
        Make a table in Query
      </button>
    </div>
  )
}

function Rows({ project, db, schema, name, onOpen, onQuery }: {
  project: string
  db: string
  schema: string
  name: string
  onOpen: (t: { schema: string; name: string }) => void
  onQuery: (draft?: string) => void
}) {
  const [offset, setOffset] = useState(0)
  const [sort, setSort] = useState<{ column: string; desc: boolean } | null>(null)
  const [where, setWhere] = useState('')
  const [typed, setTyped] = useState('')
  const [open, setOpen] = useState<number | null>(null)
  const params = new URLSearchParams({ db, offset: String(offset), limit: String(PAGE), where })
  if (sort) params.set('sort', sort.column), params.set('desc', sort.desc ? '1' : '0')
  const path = `${databasePath(project)}/tables/${encodeURIComponent(schema)}/${encodeURIComponent(name)}?${params}`
  const page = useData<DbPage>(path, () => false, 60_000)
  const p = page.data

  const filter = (e: FormEvent) => {
    e.preventDefault()
    setWhere(typed.trim())
    setOffset(0)
    setOpen(null)
  }
  const sortBy = (column: string) => {
    setSort((s) => (s?.column === column ? { column, desc: !s.desc } : { column, desc: true }))
    setOffset(0)
    setOpen(null)
  }
  const sorted = sort?.column ?? p?.sort ?? p?.key?.[0]
  const desc = sort ? sort.desc : !!p?.desc
  const last = p ? offset + p.rows.length : 0
  const more = p ? p.more || p.count > last : false

  return (
    <div className="panel db-grid-panel">
      <div className="db-grid-head">
        <span className="mono t-small t-strong">{name}</span>
        <span className="t-note grow">{p ? (p.count === 0 && !p.where ? 'No rows' : `${count(p.count, p.more)}${p.where ? ' matching' : ''} ${p.count === 1 && !p.more ? 'row' : 'rows'}`) : ''}</span>
        <form className="db-filter" onSubmit={filter}>
          <svg viewBox="0 0 24 24" aria-hidden>
            <path d="M4 6H20M7 12H17M10 18H14" />
          </svg>
          <input
            className="mono"
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
            placeholder="status = 'failed'"
            aria-label="Filter rows with SQL"
            spellCheck={false}
            autoCapitalize="off"
          />
        </form>
        {p && p.rows.length > 0 && (
          <span className="t-caption t-muted">
            {(offset + 1).toLocaleString('en-GB')}–{last.toLocaleString('en-GB')}
          </span>
        )}
        <button className="icon-btn" aria-label="Previous rows" disabled={offset === 0} onClick={() => (setOffset(Math.max(0, offset - PAGE)), setOpen(null))}>
          <ChevronLeft />
        </button>
        <button className="icon-btn" aria-label="Next rows" disabled={!more} onClick={() => (setOffset(offset + PAGE), setOpen(null))}>
          <ChevronRight />
        </button>
      </div>
      {page.error ? (
        <div className="db-grid-error">
          <span className="mono">{page.error.message}</span>
          {where && (
            <button className="btn quiet small" onClick={() => (setWhere(''), setTyped(''))}>
              Clear the filter
            </button>
          )}
        </div>
      ) : !p ? (
        <Skeleton rows={8} />
      ) : (
        <div className="db-grid-scroll">
          <table className="db-grid">
            <thead>
              <tr>
                {p.columns.map((c) => (
                  <th key={c.name} className={alignRight(p, c) ? 'num' : ''}>
                    <button onClick={() => sortBy(c.name)} data-tip={p.refs[c.name] ? `${c.type}, points to ${p.refs[c.name].table}` : c.type} data-tip-mono>
                      <span className={sorted === c.name ? 'sorted' : ''}>{c.name}</span>
                      {sorted === c.name && <span aria-hidden>{desc ? ' ↓' : ' ↑'}</span>}
                    </button>
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {p.rows.map((row, i) => (
                <tr key={offset + i} className={open === i ? 'open' : ''} onClick={() => setOpen(open === i ? null : i)}>
                  {row.map((v, j) => (
                    <td key={j} className={alignRight(p, p.columns[j]) ? 'num' : ''}>
                      <Value value={v} type={p.columns[j].type} />
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
          {p.rows.length === 0 && (
            <div className="db-grid-empty">
              <span className="t-small t-strong">{p.where ? 'No rows match' : 'No rows yet'}</span>
              {p.where ? (
                <button className="btn quiet small" onClick={() => (setWhere(''), setTyped(''))}>
                  Clear the filter
                </button>
              ) : (
                <>
                  <span className="t-caption t-muted">Rows your app adds show up here.</span>
                  <button
                    className="btn quiet small"
                    onClick={() => {
                      const cols = p.columns.map((c) => c.name).filter((c) => !p.key?.includes(c))
                      onQuery(`INSERT INTO ${schema === 'public' ? '' : schema + '.'}${name} (${cols.join(', ')})\nVALUES ();`)
                    }}
                  >
                    Insert a row with Query
                  </button>
                </>
              )}
            </div>
          )}
        </div>
      )}
      {p && open !== null && p.rows[open] && (
        <RowSheet
          page={p}
          index={open}
          name={name}
          onMove={(d) => setOpen((o) => (o === null ? o : Math.min(p.rows.length - 1, Math.max(0, o + d))))}
          onClose={() => setOpen(null)}
          onFollow={(table, column, value) => {
            onOpen({ schema: table.schema, name: table.table })
            setTimeout(() => window.dispatchEvent(new CustomEvent('pier-db-filter', { detail: `${column} = '${value.replace(/'/g, "''")}'` })), 0)
          }}
        />
      )}
      <FilterFromLink onFilter={(f) => (setTyped(f), setWhere(f), setOffset(0))} />
    </div>
  )
}

// FilterFromLink takes a filter from a row's link to another table, once
// that table's rows have opened.
function FilterFromLink({ onFilter }: { onFilter: (f: string) => void }) {
  useEffect(() => {
    const take = (e: Event) => onFilter((e as CustomEvent<string>).detail)
    window.addEventListener('pier-db-filter', take)
    return () => window.removeEventListener('pier-db-filter', take)
  }, [onFilter])
  return null
}

// RowSheet shows one row with every value in full, over the right of the
// page.
function RowSheet({ page, index, name, onMove, onClose, onFollow }: {
  page: DbPage
  index: number
  name: string
  onMove: (d: number) => void
  onClose: () => void
  onFollow: (ref: DbPage['refs'][string], column: string, value: string) => void
}) {
  const row = page.rows[index]
  const id = useMemo(() => {
    const k = page.key?.[0]
    const at = k ? page.columns.findIndex((c) => c.name === k) : -1
    return at >= 0 ? row[at] : null
  }, [page, row])
  const long = (v: string | null, type: string) => v !== null && type !== 'bytea' && (v.length > 60 || type === 'json' || type === 'jsonb' || v.includes('\n'))
  const object = Object.fromEntries(page.columns.map((c, i) => [c.name, row[i]]))
  const firstRef = page.columns.findIndex((c) => page.refs[c.name] && row[page.columns.indexOf(c)] !== null)
  const ref = firstRef >= 0 ? page.refs[page.columns[firstRef].name] : null
  const title = id ?? String(page.offset + index + 1)
  return (
    <Sheet
      label="Row"
      onClose={onClose}
      onMove={onMove}
      first={index === 0}
      last={index === page.rows.length - 1}
      title={
        <>
          <span className="mono t-note">{name}</span>
          <span className="t-small t-strong db-sheet-id" data-tip={title.length > 24 ? title : undefined} data-tip-mono>
            Row <span className="mono">{title}</span>
          </span>
        </>
      }
      foot={
        <>
          <button className="btn quiet" onClick={() => copy(JSON.stringify(object, null, 2), 'the row as JSON')}>
            Copy as JSON
          </button>
          {ref && (
            <button
              className="btn quiet db-follow"
              data-tip={`${ref.table} where ${ref.column} = ${row[firstRef]}`}
              data-tip-mono
              onClick={() => onFollow(ref, ref.column, row[firstRef]!)}
            >
              <span>Open {singular(ref.table)}</span>
              <span className="mono db-follow-value">{row[firstRef]}</span>
            </button>
          )}
        </>
      }
    >
      <div className="db-sheet-fields">
        <div className="db-fields">
          {page.columns.map((c, i) =>
            long(row[i], c.type) ? null : (
              <div key={c.name} className="db-field">
                <span className="t-quiet" data-tip={c.type} data-tip-mono>
                  {c.name}
                </span>
                <span>
                  {row[i] === null || c.type === 'bool' || c.type === 'bytea' ? <Value value={row[i]} type={c.type} /> : row[i]}
                </span>
              </div>
            ),
          )}
        </div>
        {page.columns.map((c, i) =>
          long(row[i], c.type) ? (
            <div key={c.name} className="db-long">
              <div className="db-long-head">
                <span className="t-quiet" data-tip={c.type} data-tip-mono>
                  {c.name}
                </span>
                <button className="icon-btn bare small" aria-label={`Copy ${c.name}`} data-tip="Copy" onClick={() => copy(row[i]!, c.name)}>
                  <Copy />
                </button>
              </div>
              <pre className="db-value">{c.type === 'json' || c.type === 'jsonb' ? json(pretty(row[i]!)) : row[i]}</pre>
            </div>
          ) : null,
        )}
      </div>
    </Sheet>
  )
}

// The column beside the grid (tables, or the query history) is as wide as
// its owner drags it, remembered in this browser.
const SIDE_KEY = 'pier.dbSideWidth'
const SIDE_MIN = 200
const SIDE_MAX = 480

export function useSideWidth() {
  const [width, setWidth] = useState(() => {
    try {
      const w = Number(localStorage.getItem(SIDE_KEY))
      return w >= SIDE_MIN && w <= SIDE_MAX ? w : 232
    } catch {
      return 232
    }
  })
  const keep = (w: number) => {
    const next = Math.min(SIDE_MAX, Math.max(SIDE_MIN, Math.round(w)))
    setWidth(next)
    try {
      localStorage.setItem(SIDE_KEY, String(next))
    } catch {
      // A private window forgets it.
    }
  }
  return { width, keep }
}

// SideHandle is the side column's right edge, which drags to resize it.
export function SideHandle({ side }: { side: ReturnType<typeof useSideWidth> }) {
  const drag = (e: React.PointerEvent<HTMLDivElement>) => {
    e.preventDefault()
    const handle = e.currentTarget
    const left = handle.parentElement!.getBoundingClientRect().left
    handle.setPointerCapture(e.pointerId)
    document.body.classList.add('resizing')
    // The handle sits in the gap after the column, 8px past its edge.
    const move = (m: PointerEvent) => side.keep(m.clientX - left - 8)
    const up = () => {
      handle.removeEventListener('pointermove', move)
      handle.removeEventListener('pointerup', up)
      handle.removeEventListener('pointercancel', up)
      document.body.classList.remove('resizing')
    }
    handle.addEventListener('pointermove', move)
    handle.addEventListener('pointerup', up)
    handle.addEventListener('pointercancel', up)
  }
  return (
    <div
      className="db-side-resize"
      role="separator"
      aria-orientation="vertical"
      aria-label="Resize the list"
      aria-valuenow={side.width}
      aria-valuemin={SIDE_MIN}
      aria-valuemax={SIDE_MAX}
      tabIndex={0}
      onPointerDown={drag}
      onDoubleClick={() => side.keep(232)}
      onKeyDown={(e) => {
        if (e.key === 'ArrowRight') (e.preventDefault(), side.keep(side.width + 24))
        if (e.key === 'ArrowLeft') (e.preventDefault(), side.keep(side.width - 24))
      }}
    />
  )
}
