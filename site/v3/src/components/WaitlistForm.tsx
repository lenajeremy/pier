import { useId, useRef, useState, type FormEvent } from 'react'
import { joinWaitlist, setWaitlistOutcome, useWaitlistOutcome, validateEmail } from '../lib/waitlist'
import s from './WaitlistForm.module.css'

/** The page's one action. States: typing, sending, joined, already, error.
 * `onEmail` reports what's typed, so the ending board can flip to it. */
export function WaitlistForm({ className, onEmail }: { className?: string; onEmail?: (v: string) => void }) {
  const id = useId()
  const outcome = useWaitlistOutcome()
  const [email, setEmail] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [sending, setSending] = useState(false)
  const trap = useRef<HTMLInputElement>(null)
  const input = useRef<HTMLInputElement>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (sending) return
    const problem = validateEmail(email)
    if (problem) {
      setError(problem)
      input.current?.focus()
      return
    }
    setError(null)
    setSending(true)
    const r = await joinWaitlist(email, trap.current?.value ?? '')
    setSending(false)
    if (r.kind === 'error') {
      setError(r.message)
      input.current?.focus()
    } else setWaitlistOutcome(r.kind)
  }

  if (outcome) {
    return (
      <p className={[s.result, className].filter(Boolean).join(' ')} role="status">
        {outcome === 'joined'
          ? "You're on the board. We'll email you once, when Pier ships."
          : "That address is already on the list. Nothing else to do."}
      </p>
    )
  }

  return (
    <form className={[s.wrap, className].filter(Boolean).join(' ')} onSubmit={submit} noValidate>
      <div className={s.field} data-invalid={error ? '' : undefined}>
        <label htmlFor={`${id}-e`} className="visually-hidden">
          Email address
        </label>
        <input
          ref={input}
          id={`${id}-e`}
          className={s.input}
          type="email"
          name="email"
          inputMode="email"
          autoComplete="email"
          autoCapitalize="none"
          spellCheck={false}
          placeholder="Email address"
          value={email}
          readOnly={sending}
          aria-invalid={error ? true : undefined}
          aria-describedby={error ? `${id}-err` : undefined}
          onChange={(e) => {
            setEmail(e.target.value)
            onEmail?.(e.target.value)
            if (error) setError(null)
          }}
        />
        <button className={s.button} type="submit" disabled={sending}>
          {sending ? 'Joining' : 'Join the waitlist'}
        </button>
      </div>
      <input ref={trap} className={s.trap} type="text" name="website" tabIndex={-1} autoComplete="off" aria-hidden="true" />
      <p id={`${id}-err`} className={s.error} aria-live="polite">
        {error}
      </p>
    </form>
  )
}
