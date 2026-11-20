import { useCallback, useEffect, useRef, useState } from 'react'
import { api, ApiError } from './api'

// useData loads a path and, while poll() says so, asks again every few
// seconds, so a build's status changes on screen without a reload.
export function useData<T>(path: string | null, poll?: (data: T) => boolean, every = 2000) {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState<ApiError | null>(null)
  const pollRef = useRef(poll)
  pollRef.current = poll
  const seqRef = useRef(0)
  const lastDataRef = useRef<T | null>(null)

  const load = useCallback(async (signal?: AbortSignal) => {
    if (!path) return { data: null as T | null, error: null as ApiError | null }
    const seq = ++seqRef.current
    try {
      const d = await api<T>(path, { signal })
      if (seq !== seqRef.current) return { data: null as T | null, error: null as ApiError | null }
      lastDataRef.current = d
      setData(d)
      setError(null)
      return { data: d, error: null as ApiError | null }
    } catch (e) {
      if (signal?.aborted || seq !== seqRef.current) return { data: null as T | null, error: null as ApiError | null }
      const err = e instanceof ApiError ? e : new ApiError(0, { error: String(e) })
      setError(err)
      return { data: null as T | null, error: err }
    }
  }, [path])

  useEffect(() => {
    lastDataRef.current = null
    setData(null)
    setError(null)
    let stopped = false
    let timer: number | undefined
    const ac = new AbortController()
    const tick = async () => {
      const { data: d, error: err } = await load(ac.signal)
      if (stopped) return
      if (d && pollRef.current?.(d)) {
        timer = window.setTimeout(tick, every)
      } else if (err && (err.status === 0 || err.status >= 500) && pollRef.current) {
        const shouldRetry = lastDataRef.current ? pollRef.current(lastDataRef.current) : true
        if (shouldRetry) timer = window.setTimeout(tick, Math.min(every * 2, 10_000))
      }
    }
    tick()
    return () => {
      stopped = true
      ac.abort()
      window.clearTimeout(timer)
    }
  }, [load, every])

  const reload = useCallback(() => load().then((r) => r.data), [load])
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
