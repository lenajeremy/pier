import { useCallback, useState } from 'react'

// Spinner is something under way (the loading button of Components 1): a
// faint ring with its top quarter in the ink of what holds it, turning at an
// even speed. 13px inside a button, a badge or a line of text; `large` is
// the 16px one that stands alone in a panel. With reduced motion it doesn't
// turn, it only fades a little in and out.
export function Spinner({ large, label, className }: {
  large?: boolean
  label?: string // standing alone, what's under way; beside words it's hidden
  className?: string
}) {
  const s = large ? 16 : 13
  const w = 1.5
  const c = s / 2
  const r = (s - w) / 2
  const d = r * Math.SQRT1_2
  return (
    <svg
      className={'spinner' + (className ? ' ' + className : '')}
      viewBox={`0 0 ${s} ${s}`}
      width={s}
      height={s}
      fill="none"
      stroke="currentColor"
      strokeWidth={w}
      role={label ? 'img' : undefined}
      aria-label={label}
      aria-hidden={label ? undefined : true}
    >
      <circle cx={c} cy={c} r={r} strokeOpacity={0.35} />
      <path d={`M${c - d} ${c - d}A${r} ${r} 0 0 1 ${c + d} ${c - d}`} />
    </svg>
  )
}

// useBusy is for buttons that start a request without asking first: it says
// which one is running, so that one shows the spinner and the rest wait.
export function useBusy<K = string>() {
  const [busy, setBusy] = useState<K | null>(null)
  const run = useCallback(async (key: K, work: () => Promise<unknown>) => {
    setBusy(key)
    try {
      await work()
    } finally {
      setBusy(null)
    }
  }, [])
  return [busy, run] as const
}
