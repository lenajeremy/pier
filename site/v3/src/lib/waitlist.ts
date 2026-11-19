import { useSyncExternalStore } from 'react'

// Mirrors NormalizeEmail in server/internal/waitlist/email.go. The server
// has the final say; this only saves a round trip for obvious typos.
export function validateEmail(raw: string): string | null {
  const s = raw.trim()
  if (s === '') return 'Enter your email address.'
  if (s.length > 254) return 'That email address is too long.'
  if (!s.includes('@')) return 'That email address is missing an @, like sam@homelab.dev.'
  const at = s.lastIndexOf('@')
  const local = s.slice(0, at)
  const domain = s.slice(at + 1)
  if (local === '') return 'That email address is missing the part before the @.'
  const dot = domain.lastIndexOf('.')
  if (domain === '' || dot <= 0 || domain.length - dot - 1 < 2) {
    return 'That email address is missing its domain, like .com or .dev.'
  }
  if (/[\s<>,;]/.test(s) || local.includes('@')) {
    return "That doesn't look like an email address. Check it for typos."
  }
  return null
}

export type JoinResult =
  | { kind: 'joined' }
  | { kind: 'already' }
  | { kind: 'error'; message: string }

export async function joinWaitlist(email: string, website: string): Promise<JoinResult> {
  let res: Response
  try {
    res = await fetch('/api/waitlist', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email: email.trim(), website }),
    })
  } catch {
    return { kind: 'error', message: "Couldn't reach the server. Check your connection and try again." }
  }
  const body = (await res.json().catch(() => ({}))) as { status?: string; error?: string }
  if (res.ok && body.status === 'joined') return { kind: 'joined' }
  if (res.ok && body.status === 'already') return { kind: 'already' }
  return { kind: 'error', message: body.error ?? 'Something went wrong on our side. Try again in a minute.' }
}

// One outcome for the whole page: joining in the hero also settles the form
// in the waitlist band, so nobody is asked twice.
type Outcome = 'joined' | 'already' | null
let outcome: Outcome = null
const listeners = new Set<() => void>()

export function setWaitlistOutcome(next: Outcome) {
  outcome = next
  listeners.forEach((l) => l())
}

export function useWaitlistOutcome(): Outcome {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l)
      return () => listeners.delete(l)
    },
    () => outcome,
    () => null,
  )
}
