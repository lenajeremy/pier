import { useEffect, useRef, useState, useSyncExternalStore, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { Copy, Done, Warning } from './Icons'

// A toast confirms what just happened (Components 4): bottom right, for five
// seconds, longer while the pointer is over it, with the undo when there is one.

type Tone = 'done' | 'failed' | 'copied'
type Toast = {
  id: number
  text: ReactNode
  tone: Tone
  action?: { label: string; run: () => void }
}

let toasts: Toast[] = []
let next = 1
const watchers = new Set<() => void>()
const tell = () => watchers.forEach((w) => w())

export function toast(text: ReactNode, options: { tone?: Tone; action?: Toast['action'] } = {}) {
  toasts = [...toasts.slice(-2), { id: next++, text, tone: options.tone ?? 'done', action: options.action }]
  tell()
}

const dismiss = (id: number) => {
  toasts = toasts.filter((t) => t.id !== id)
  tell()
}

// copy puts text on the clipboard and says so.
export async function copy(text: string, what = 'the address') {
  try {
    await navigator.clipboard.writeText(text)
    toast(`Copied ${what}`, { tone: 'copied' })
  } catch {
    toast("Couldn't copy. Select the text and copy it yourself.", { tone: 'failed' })
  }
}

export function ToastHost() {
  const list = useSyncExternalStore(
    (w) => {
      watchers.add(w)
      return () => watchers.delete(w)
    },
    () => toasts,
  )
  return createPortal(
    <div className="toasts" role="status" aria-live="polite">
      {list.map((t) => (
        <ToastCard key={t.id} toast={t} />
      ))}
    </div>,
    document.body,
  )
}

function ToastCard({ toast: t }: { toast: Toast }) {
  const [held, setHeld] = useState(false)
  const left = useRef(t.tone === 'failed' ? 8000 : 5000)
  useEffect(() => {
    if (held) return
    const started = Date.now()
    const timer = window.setTimeout(() => dismiss(t.id), left.current)
    return () => {
      window.clearTimeout(timer)
      left.current = Math.max(1500, left.current - (Date.now() - started))
    }
  }, [held, t.id])
  return (
    <div
      className={'toast ' + t.tone}
      onMouseEnter={() => setHeld(true)}
      onMouseLeave={() => setHeld(false)}
      onFocus={() => setHeld(true)}
      onBlur={() => setHeld(false)}
    >
      {t.tone === 'failed' ? <Warning size={18} /> : t.tone === 'copied' ? <Copy size={18} /> : <Done size={18} />}
      <span className="toast-text">{t.text}</span>
      {t.action && (
        <button
          className="btn quiet small"
          onClick={() => {
            dismiss(t.id)
            t.action!.run()
          }}
        >
          {t.action.label}
        </button>
      )}
    </div>
  )
}
