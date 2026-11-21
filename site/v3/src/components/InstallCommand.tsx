import { useEffect, useState } from 'react'
import s from './InstallCommand.module.css'

/** Where the page is served from, so the commands name this site: it sends
 * /install and /cli on to the installers in the repository. */
export const origin = typeof window === 'undefined' ? 'https://pier.example.com' : window.location.origin

export const commands = {
  server: `curl -fsSL ${origin}/install | sudo sh`,
  cli: `curl -fsSL ${origin}/cli | sh`,
}

/** The command's parts: what runs, where it comes from, and the pipe. */
function split(cmd: string): string[] {
  const m = cmd.match(/^(\S+ \S+) (\S+) (\| .+)$/)
  return m ? [m[1], m[2], m[3]] : [cmd]
}

/** The page's one action: a command to copy. It sits in the pill the
 * waitlist used, with the prompt dimmed and the button saying what it did.
 * `onCopy` lets the ending board flip when the command is taken. */
export function InstallCommand({ cmd, label, className, onCopy }: { cmd: string; label: string; className?: string; onCopy?: () => void }) {
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    if (!copied) return
    const t = setTimeout(() => setCopied(false), 2000)
    return () => clearTimeout(t)
  }, [copied])

  async function copy() {
    try {
      await navigator.clipboard.writeText(cmd)
    } catch {
      // Without clipboard access the command is still there to select.
      const range = document.createRange()
      const code = document.getElementById(label)
      if (!code) return
      range.selectNodeContents(code)
      const sel = window.getSelection()
      sel?.removeAllRanges()
      sel?.addRange(range)
      return
    }
    setCopied(true)
    onCopy?.()
  }

  return (
    <div className={[s.field, className].filter(Boolean).join(' ')}>
      <span className={s.prompt} aria-hidden="true">
        $
      </span>
      <code id={label} className={s.code} aria-label={label}>
        <span className={s.line}>{cmd}</span>
        {/* A phone can't fit the command on one line, so it's set the way a
            shell script splits a long one: a part a line, each continued
            with a backslash. Copying still takes the one-line command. */}
        <span className={s.lines} aria-hidden="true">
          {split(cmd).map((part, i, all) => (
            <span key={i} className={i ? s.cont : undefined}>
              {part}
              {i < all.length - 1 ? ' \\' : ''}
            </span>
          ))}
        </span>
      </code>
      <button type="button" className={s.button} onClick={copy} aria-label={copied ? 'Copied' : `Copy: ${label}`}>
        {copied ? 'Copied' : 'Copy'}
      </button>
      <span className="visually-hidden" aria-live="polite">
        {copied ? 'Command copied' : ''}
      </span>
    </div>
  )
}
