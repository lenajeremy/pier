import { useCallback, useEffect, useRef, useState } from 'react'
import { api, ApiError } from './api'

// Every is how long to wait between polls, in milliseconds: one number, or a
// function of the latest data, so a page can ask often while something is
// changing and rarely otherwise.
export type Every<T> = number | ((data: T) => number)

// Loaded is what a load came to. Superseded means a newer load started, or
// the page went away, before this one answered, so its answer was dropped.
// That's neither data nor an error, and it mustn't end the polling.
type Loaded<T> = { data: T | null; error: ApiError | null; superseded: boolean }

const defaultEvery = 2000

// useData loads a path and, while poll() says so, asks again every few
// seconds, so a build's status changes on screen without a reload. A hidden
// tab doesn't poll; it asks again as soon as it's shown.
export function useData<T>(path: string | null, poll?: (data: T) => boolean, every: Every<T> = defaultEvery) {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState<ApiError | null>(null)
  const pollRef = useRef(poll)
  pollRef.current = poll
  const everyRef = useRef(every)
  everyRef.current = every
  const seqRef = useRef(0)
  const lastDataRef = useRef<T | null>(null)
  // The mounted effect's plan, so a manual reload sets the next poll too.
  const planRef = useRef<((r: Loaded<T>) => void) | null>(null)

  const load = useCallback(async (signal?: AbortSignal): Promise<Loaded<T>> => {
    if (!path) return { data: null, error: null, superseded: false }
    const seq = ++seqRef.current
    const superseded: Loaded<T> = { data: null, error: null, superseded: true }
    try {
      const d = await api<T>(path, { signal })
      if (seq !== seqRef.current) return superseded
      lastDataRef.current = d
      setData(d)
      setError(null)
      return { data: d, error: null, superseded: false }
    } catch (e) {
      if (signal?.aborted || seq !== seqRef.current) return superseded
      const err = e instanceof ApiError ? e : new ApiError(0, { error: String(e) })
      setError(err)
      return { data: null, error: err, superseded: false }
    }
  }, [path])

  useEffect(() => {
    lastDataRef.current = null
    setData(null)
    setError(null)
    let stopped = false
    let asleep = false // a poll fell due while the tab was hidden
    let timer: number | undefined
    const ac = new AbortController()

    // wait is how long until the next poll, or null for none. It goes by the
    // latest data, whichever load brought it, so a poll that a reload
    // superseded keeps the schedule. Network and server errors try again,
    // more slowly; any other error stops.
    const wait = (r: Loaded<T>): number | null => {
      const poll = pollRef.current
      const latest = lastDataRef.current
      if (!poll || (latest && !poll(latest))) return null
      const e = everyRef.current
      const ms = typeof e !== 'function' ? e : latest ? e(latest) : defaultEvery
      if (r.error) return r.error.status === 0 || r.error.status >= 500 ? Math.min(ms * 2, 10_000) : null
      return r.data || r.superseded ? ms : null
    }
    // plan replaces the pending poll, so there's never more than one.
    const plan = (r: Loaded<T>) => {
      window.clearTimeout(timer)
      asleep = false
      const ms = wait(r)
      timer = ms === null ? undefined : window.setTimeout(due, ms)
    }
    const tick = async () => {
      const r = await load(ac.signal)
      if (!stopped) plan(r)
    }
    const due = () => {
      if (document.hidden) asleep = true
      else tick()
    }
    const shown = () => {
      if (document.hidden || !asleep) return
      asleep = false
      tick()
    }
    planRef.current = plan
    document.addEventListener('visibilitychange', shown)
    tick()
    return () => {
      stopped = true
      planRef.current = null
      seqRef.current++ // drops a reload still in flight, as the abort drops a poll
      ac.abort()
      window.clearTimeout(timer)
      document.removeEventListener('visibilitychange', shown)
    }
  }, [load])

  // reload asks now, after a change made on the page. What it brings back
  // decides the next poll, so a reload that finds a build running is followed.
  // Asked for once the page has gone, say when a slow action ends, it does nothing.
  const reload = useCallback(async () => {
    if (!planRef.current) return null
    const r = await load()
    if (!r.superseded) planRef.current?.(r)
    return r.data
  }, [load])
  return { data, error, reload }
}

// useNow re-renders every interval, so "2 minutes ago" stays true.
export function useNow(every = 30_000): number {
  const [now, setNow] = useState(Date.now())
  useEffect(() => {
    const t = window.setInterval(() => setNow(Date.now()), every)
    return () => window.clearInterval(t)
  }, [every])
  return now
}
