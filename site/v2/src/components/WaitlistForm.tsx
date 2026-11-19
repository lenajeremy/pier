import { useId, useRef, useState, type FormEvent } from 'react'
import { joinWaitlist, setWaitlistOutcome, useWaitlistOutcome, validateEmail } from '../lib/waitlist'
import { CheckIcon } from './icons'
import s from './WaitlistForm.module.css'

// The page's one job. States follow the design system: default, typing,
// sending ("Joining"), joined, error, and already on the list.
export function WaitlistForm({ tone = 'light', className }: { tone?: 'light' | 'dark'; className?: string }) {
  const id = useId()
  const outcome = useWaitlistOutcome()
  const [email, setEmail] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [sending, setSending] = useState(false)
  const honeypot = useRef<HTMLInputElement>(null)
  const input = useRef<HTMLInputElement>(null)

  async function onSubmit(e: FormEvent) {
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
    const result = await joinWaitlist(email, honeypot.current?.value ?? '')
    setSending(false)
    if (result.kind === 'error') {
      setError(result.message)
      input.current?.focus()
    } else {
      setWaitlistOutcome(result.kind)
    }
  }

  const classes = [s.wrap, tone === 'dark' && s.dark, className].filter(Boolean).join(' ')

  if (outcome) {
    return (
      <div className={classes}>
        <p className={`${s.result} ${outcome === 'joined' ? s.joined : s.already}`} role="status">
          {outcome === 'joined' ? (
            <>
              <CheckIcon size={16} />
              You're on the list. We'll email you once, when Pier ships.
            </>
          ) : (
            "That address is already on the list. Nothing else to do."
          )}
        </p>
      </div>
    )
  }

  return (
    <form className={classes} onSubmit={onSubmit} noValidate>
      <div className={s.field} data-invalid={error ? '' : undefined}>
        <label htmlFor={`${id}-email`} className="visually-hidden">
          Email address
        </label>
        <input
          ref={input}
          id={`${id}-email`}
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
          aria-describedby={error ? `${id}-error` : undefined}
          onChange={(e) => {
            setEmail(e.target.value)
            if (error) setError(null)
          }}
        />
        <button className={s.button} type="submit" disabled={sending}>
          {sending ? (
            <>
              <span className={s.spinner} aria-hidden="true" />
              Joining
            </>
          ) : (
            'Join the waitlist'
          )}
        </button>
      </div>
      {/* Hidden from people; bots that fill every field get quietly dropped. */}
      <input ref={honeypot} className={s.trap} type="text" name="website" tabIndex={-1} autoComplete="off" aria-hidden="true" />
      <p id={`${id}-error`} className={s.error} aria-live="polite">
        {error}
      </p>
    </form>
  )
}
