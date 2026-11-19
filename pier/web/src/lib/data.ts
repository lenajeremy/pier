import { useCallback, useEffect, useRef, useState } from 'react'
import { api, ApiError } from './api'

// useData loads a path and, while poll() says so, asks again every few
// seconds, so a build's status changes on screen without a reload.
export function useData<T>(path: string | null, poll?: (data: T) => boolean, every = 2000) {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState<ApiError | null>(null)
  const pollRef = useRef(poll)
  pollRef.current = poll

  const load = useCallback(async () => {
    if (!path) return null
    try {
      const d = await api<T>(path)
      setData(d)
      setError(null)
      return d
    } catch (e) {
      setError(e instanceof ApiError ? e : new ApiError(0, { error: String(e) }))
      return null
    }
  }, [path])

  useEffect(() => {
    setData(null)
    setError(null)
    let stopped = false
    let timer: number | undefined
    const tick = async () => {
      const d = await load()
      if (stopped) return
      if (d && pollRef.current?.(d)) timer = window.setTimeout(tick, every)
    }
    tick()
    return () => {
      stopped = true
      window.clearTimeout(timer)
    }
  }, [load, every])

  return { data, error, reload: load }
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
