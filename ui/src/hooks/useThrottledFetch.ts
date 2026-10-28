import { useEffect, useEffectEvent, useRef, useState } from 'react'

/** What a read returned: null while unknown, or why it could not be read. */
export interface Fetched<T> {
  data: T | null
  error?: string
}

/** The least time between the starts of two reads of the same key. */
const intervalMs = 2000

/**
 * useThrottledFetch reads load() for key, and again whenever version
 * changes: at once when the last read of key started two seconds ago or
 * more, else two seconds after it, with what is current by then. A burst of
 * changes costs at most one read every two seconds, and its last change is
 * always read. An empty key reads nothing. A response that arrives after a
 * newer one is dropped.
 */
export function useThrottledFetch<T>(key: string, version: string, load: () => Promise<T>): Fetched<T> {
  const [read, setRead] = useState<{ key: string; data: T | null; error?: string } | null>(null)
  const run = useEffectEvent(load)
  // The key and start of the last read, and the sequence numbers of the
  // last read started and of the last one shown.
  const last = useRef({ key: '', startedAt: 0, started: 0, shown: 0 })

  useEffect(() => {
    if (!key) return
    const since = last.current.key === key ? Date.now() - last.current.startedAt : intervalMs
    const timer = setTimeout(() => {
      const seq = ++last.current.started
      last.current.key = key
      last.current.startedAt = Date.now()
      const show = (data: T | null, error?: string) => {
        if (seq < last.current.shown) return
        last.current.shown = seq
        setRead({ key, data, error })
      }
      run().then(
        (data) => show(data),
        (err: unknown) => show(null, err instanceof Error ? err.message : String(err)),
      )
    }, Math.max(0, intervalMs - since))
    return () => clearTimeout(timer)
  }, [key, version])

  if (!read || read.key !== key) return { data: null }
  return read.error !== undefined ? { data: null, error: read.error } : { data: read.data }
}
