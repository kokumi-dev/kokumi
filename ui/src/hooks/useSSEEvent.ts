import { useEffect, useState } from 'react'

/**
 * Subscribes to a single named event type on an SSE endpoint and returns the
 * latest parsed value, or null until the first event is received.
 *
 * The browser's EventSource automatically reconnects on network interruptions.
 * Cleanup closes the connection when the component unmounts or the arguments change.
 *
 * @param endpoint  The SSE URL to connect to, e.g. '/api/v1/events'.
 * @param eventType The named SSE event to listen for, e.g. 'counts'.
 *
 * @example
 * const counts = useSSEEvent<ResourceCounts>('/api/v1/events', 'counts')
 */
export function useSSEEvent<T>(endpoint: string, eventType: string): T | null {
  const [value, setValue] = useState<T | null>(null)

  useEffect(() => {
    const es = new EventSource(endpoint)

    es.addEventListener(eventType, (event: MessageEvent<string>) => {
      try {
        setValue(JSON.parse(event.data) as T)
      } catch {
        // ignore malformed events
      }
    })

    return () => {
      es.close()
    }
  }, [endpoint, eventType])

  return value
}
