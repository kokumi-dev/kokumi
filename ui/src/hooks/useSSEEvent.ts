import { useEffect, useRef, useState } from 'react'
import { withAccessToken, refresh } from '../api/auth'

/**
 * Subscribes to a single named event type on an SSE endpoint and returns the
 * latest parsed value, or null until the first event is received.
 *
 * The browser's EventSource automatically reconnects on network interruptions,
 * but it STOPS reconnecting on HTTP 4xx responses (e.g. a 401 once the access
 * token expires). To keep the dashboard alive past token expiry we listen for
 * the SSE `error` event, silently refresh the access token, and recreate the
 * EventSource with the new token.
 *
 * @param endpoint  The SSE URL to connect to, e.g. '/api/v1/events'.
 * @param eventType The named SSE event to listen for, e.g. 'counts'.
 *
 * @example
 * const counts = useSSEEvent<ResourceCounts>('/api/v1/events', 'counts')
 */
export function useSSEEvent<T>(endpoint: string, eventType: string): T | null {
  const [value, setValue] = useState<T | null>(null)
  const esRef = useRef<EventSource | null>(null)

  useEffect(() => {
    let cancelled = false

    const connect = () => {
      // EventSource cannot set an Authorization header, so the token is passed
      // as a query parameter, which the server also accepts.
      const es = new EventSource(withAccessToken(endpoint))
      esRef.current = es

      es.addEventListener(eventType, (event: MessageEvent<string>) => {
        try {
          setValue(JSON.parse(event.data) as T)
        } catch {
          // ignore malformed events
        }
      })

      es.addEventListener('error', () => {
        // The connection is dead (typically a 401 on token expiry). Close it,
        // refresh the access token, and reconnect with the new token. If the
        // refresh fails the token is cleared and the app shows the login screen.
        es.close()
        if (cancelled) return
        void refresh().then((ok) => {
          if (ok && !cancelled) connect()
        })
      })
    }

    connect()

    return () => {
      cancelled = true
      esRef.current?.close()
    }
  }, [endpoint, eventType])

  return value
}
