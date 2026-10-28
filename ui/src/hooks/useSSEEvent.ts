import { useCallback, useSyncExternalStore } from 'react'
import { withAccessToken, refresh, onAuthChange, getToken } from '../api/auth'

/** The SSE event types the server streams on /api/v1/events. */
export type SSEEventType =
  | 'counts'
  | 'orders'
  | 'preparations'
  | 'servings'
  | 'products'
  | 'registries'
  | 'repositories'
  | 'environments'
  | 'clusters'

const ENDPOINT = '/api/v1/events'
const EVENT_TYPES: SSEEventType[] = [
  'counts',
  'orders',
  'preparations',
  'servings',
  'products',
  'registries',
  'repositories',
  'environments',
  'clusters',
]

// Keep the stream open briefly after the last subscriber leaves, so navigating
// between pages (unmount, then mount) reuses it instead of reconnecting.
const IDLE_CLOSE_MS = 5000

// Reconnects after the first back off exponentially up to this delay, so a
// failing server or network is not hit in a tight loop.
const RETRY_BASE_MS = 1000
const RETRY_MAX_MS = 30_000

// The server sends a heartbeat every 15 s. A stream silent for much longer is
// dead even when the browser has not noticed, e.g. a connection that hangs
// after the server pod went away, so it is closed and reconnected.
const STALE_AFTER_MS = 40_000

// One EventSource per tab, shared by every hook. The server sends all event
// types on one connection, in a fixed order, so a single stream delivers
// related lists in step and costs one connection instead of one per hook.
let source: EventSource | null = null
let closeTimer: ReturnType<typeof setTimeout> | undefined
// Set from a stream error until the reconnect, so subscribers that mount in
// between wait for it instead of connecting on their own.
let reconnecting = false
let retryTimer: ReturnType<typeof setTimeout> | undefined
let retries = 0
let lastSeen = 0
let watchdog: ReturnType<typeof setTimeout> | undefined
const raw = new Map<SSEEventType, string>()
const values = new Map<SSEEventType, unknown>()
const listeners = new Map<SSEEventType, Set<() => void>>()

function subscriberCount(): number {
  let n = 0
  listeners.forEach((set) => (n += set.size))
  return n
}

function notify(type: SSEEventType): void {
  listeners.get(type)?.forEach((l) => l())
}

/** Delay before the next reconnect: none for the first, then exponential with jitter. */
function retryDelay(): number {
  if (retries === 0) return 0
  const max = Math.min(RETRY_MAX_MS, RETRY_BASE_MS * 2 ** (retries - 1))
  return max / 2 + Math.random() * (max / 2)
}

/** Records that the stream works: the server replays its snapshots on connect and sends heartbeats. */
function alive(): void {
  lastSeen = Date.now()
  retries = 0
}

/**
 * Fails the stream once it has been silent for STALE_AFTER_MS. The check
 * compares timestamps instead of trusting a single timer, because browsers
 * delay timers in background tabs.
 */
function watch(es: EventSource): void {
  clearTimeout(watchdog)
  watchdog = setTimeout(
    () => {
      if (source !== es) return
      if (Date.now() - lastSeen >= STALE_AFTER_MS) fail(es)
      else watch(es)
    },
    STALE_AFTER_MS - (Date.now() - lastSeen),
  )
}

function connect(): void {
  // EventSource cannot set an Authorization header, so the token is passed
  // as a query parameter, which the server also accepts.
  const es = new EventSource(withAccessToken(ENDPOINT))
  source = es
  lastSeen = Date.now()
  watch(es)

  es.addEventListener('heartbeat', alive)
  for (const type of EVENT_TYPES) {
    es.addEventListener(type, (event: MessageEvent<string>) => {
      alive()
      // Identical payloads keep the previous value, so they cause no render.
      if (raw.get(type) === event.data) return
      try {
        values.set(type, JSON.parse(event.data))
        raw.set(type, event.data)
      } catch {
        return // ignore malformed events
      }
      notify(type)
    })
  }

  es.addEventListener('error', () => fail(es))
}

/**
 * Closes a failed stream and reconnects. The browser's EventSource reconnects
 * on network interruptions, but STOPS reconnecting on HTTP 4xx responses
 * (e.g. a 401 once the access token expires). So the access token is
 * refreshed first and the stream reopened with the new token. If the session
 * is over the refresh clears the token, the app shows the login screen, and
 * onAuthChange below disconnects. Otherwise, e.g. while the server is
 * unreachable, it keeps retrying with backoff.
 */
function fail(es: EventSource): void {
  es.close()
  if (source !== es) return
  clearTimeout(watchdog)
  source = null
  reconnecting = true
  void refresh().then(() => {
    if (!reconnecting || !getToken()) return
    retryTimer = setTimeout(() => {
      retryTimer = undefined
      reconnecting = false
      if (source === null && subscriberCount() > 0) connect()
    }, retryDelay())
    retries++
  })
}

function disconnect(): void {
  clearTimeout(closeTimer)
  closeTimer = undefined
  clearTimeout(retryTimer)
  retryTimer = undefined
  clearTimeout(watchdog)
  reconnecting = false
  retries = 0
  source?.close()
  source = null
  // Without a live stream the cached values go stale; start from "loading"
  // on the next connect instead of showing them.
  raw.clear()
  const types = [...values.keys()]
  values.clear()
  types.forEach(notify)
}

// Logging out ends the stream and drops data visible to the previous user.
onAuthChange(() => {
  if (!getToken()) disconnect()
})

function subscribe(type: SSEEventType, listener: () => void): () => void {
  clearTimeout(closeTimer)
  closeTimer = undefined
  let set = listeners.get(type)
  if (!set) {
    set = new Set()
    listeners.set(type, set)
  }
  set.add(listener)
  if (!source && !reconnecting) connect()

  return () => {
    set.delete(listener)
    if (subscriberCount() === 0) {
      closeTimer = setTimeout(disconnect, IDLE_CLOSE_MS)
    }
  }
}

/**
 * Returns the latest value of one SSE event type from the shared
 * /api/v1/events stream, or null until the first event is received.
 *
 * @example
 * const counts = useSSEEvent<ResourceCounts>('counts')
 */
export function useSSEEvent<T>(eventType: SSEEventType): T | null {
  const sub = useCallback((listener: () => void) => subscribe(eventType, listener), [eventType])
  const snapshot = useCallback(() => (values.get(eventType) as T | undefined) ?? null, [eventType])
  return useSyncExternalStore(sub, snapshot)
}
