import { useEffect, useId, useRef, useState, useSyncExternalStore, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { ApiError } from '../lib/api'

// Dialog is the Confirmation and the Form dialog (Components 4): the page
// behind dims, focus stays inside until it closes, and Escape or a click on
// the dimmed page closes it.
export function Dialog({ title, children, footer, onClose, wide, busy }: {
  title: ReactNode
  children?: ReactNode
  footer: ReactNode
  onClose: () => void
  wide?: boolean // a form, rather than a question
  busy?: boolean // an action is running: closing waits for it
}) {
  const panel = useRef<HTMLDivElement>(null)
  const heading = useId()

  useEffect(() => {
    const before = document.activeElement as HTMLElement | null
    const node = panel.current!
    // A form starts in its first field; a question starts on Cancel.
    const first =
      node.querySelector<HTMLElement>('[autofocus], .dialog-body input, .dialog-body textarea, .dialog-body select') ??
      node.querySelector<HTMLElement>('.dialog-foot button')
    first?.focus()
    const overflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => {
      document.body.style.overflow = overflow
      before?.focus?.()
    }
  }, [])

  const key = (e: React.KeyboardEvent) => {
    if (e.key === 'Escape') {
      e.stopPropagation()
      if (!busy) onClose()
      return
    }
    if (e.key !== 'Tab') return
    const items = [...panel.current!.querySelectorAll<HTMLElement>('button, [href], input, select, textarea')].filter(
      (el) => !el.hasAttribute('disabled') && el.tabIndex !== -1,
    )
    if (items.length === 0) return
    const first = items[0]
    const last = items[items.length - 1]
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault()
      last.focus()
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault()
      first.focus()
    }
  }

  return createPortal(
    <div className="scrim" onMouseDown={(e) => e.target === e.currentTarget && !busy && onClose()} onKeyDown={key}>
      <div ref={panel} className={'dialog' + (wide ? ' wide' : '')} role="dialog" aria-modal="true" aria-labelledby={heading}>
        <div className="dialog-text">
          <h2 id={heading} className="t-title">
            {title}
          </h2>
          {children && <div className="dialog-body">{children}</div>}
        </div>
        <div className="dialog-foot">{footer}</div>
      </div>
    </div>,
    document.body,
  )
}

// ---- Confirmation -----------------------------------------------------------

type Question = {
  title: ReactNode // the question: "Roll back blog?"
  body?: ReactNode // exactly what will happen
  action: string // the verb again: "Roll back"
  working?: string // while it runs: "Rolling back…"
  danger?: boolean // it deletes something
  run?: () => Promise<unknown> // done while the dialog is open, so a failure shows here
}

type Asked = Question & { resolve: (ok: boolean) => void }

let asked: Asked | null = null
const watchers = new Set<() => void>()
const tell = () => watchers.forEach((w) => w())

// confirm asks before something that changes what visitors see, or deletes
// something. It answers true once the action has been run.
export function confirm(q: Question): Promise<boolean> {
  return new Promise((resolve) => {
    asked?.resolve(false)
    asked = { ...q, resolve }
    tell()
  })
}

// ConfirmHost draws the one question that's open. It sits once, at the root.
export function ConfirmHost() {
  const q = useSyncExternalStore(
    (w) => {
      watchers.add(w)
      return () => watchers.delete(w)
    },
    () => asked,
  )
  if (!q) return null
  return <Confirm key={String(q.title) + q.action} q={q} />
}

function Confirm({ q }: { q: Asked }) {
  const [busy, setBusy] = useState(false)
  const [failed, setFailed] = useState('')
  const close = (ok: boolean) => {
    q.resolve(ok)
    if (asked === q) asked = null
    tell()
  }
  const go = async () => {
    if (!q.run) return close(true)
    setBusy(true)
    setFailed('')
    try {
      await q.run()
      close(true)
    } catch (e) {
      setFailed(e instanceof ApiError || e instanceof Error ? e.message : "Can't reach pierd.")
      setBusy(false)
    }
  }
  return (
    <Dialog
      title={q.title}
      busy={busy}
      onClose={() => close(false)}
      footer={
        <>
          <button className="btn quiet medium" disabled={busy} onClick={() => close(false)}>
            Cancel
          </button>
          <button className={'btn medium ' + (q.danger ? 'danger' : 'primary')} disabled={busy} onClick={go}>
            {busy ? (q.working ?? q.action) : q.action}
          </button>
        </>
      }
    >
      {q.body}
      {failed && (
        <p className="field-message" role="alert">
          {failed}
        </p>
      )}
    </Dialog>
  )
}
