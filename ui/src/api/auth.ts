// Lightweight auth state for the UI. The access token is kept in sessionStorage
// so it survives reloads within a tab but is cleared when the tab closes. The
// refresh token is held by the server in an HttpOnly cookie and is never
// exposed to JS. Subscribers are notified whenever the token changes
// (login / logout / 401 / silent refresh).

const TOKEN_KEY = 'kokumi.token'

let token: string | null = sessionStorage.getItem(TOKEN_KEY)
const listeners = new Set<() => void>()

export function getToken(): string | null {
  return token
}

export function setToken(next: string | null): void {
  token = next
  if (next) {
    sessionStorage.setItem(TOKEN_KEY, next)
  } else {
    sessionStorage.removeItem(TOKEN_KEY)
  }
  listeners.forEach((l) => l())
}

/** Subscribe to token changes. Returns an unsubscribe function. */
export function onAuthChange(listener: () => void): () => void {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

// decodeTokenPayload returns the parsed JWT payload, or null if the token is
// missing or unreadable. Used to derive expiry without a server-provided TTL.
function decodeTokenPayload(tokenToCheck: string | null = token): { exp?: number } | null {
  if (!tokenToCheck) return null
  const parts = tokenToCheck.split('.')
  if (parts.length < 2) return null
  try {
    return JSON.parse(atob(parts[1])) as { exp?: number }
  } catch {
    return null
  }
}

/**
 * Returns the remaining access-token lifetime in seconds, derived from the
 * token's own `exp` claim. The proactive refresh timer uses this so the UI
 * renews before expiry without the server advertising the TTL. Returns 0 when
 * there is no token or it is unreadable/expired.
 */
export function getTokenTTL(): number {
  const payload = decodeTokenPayload()
  if (typeof payload?.exp !== 'number') return 0
  return Math.max(0, payload.exp - Math.floor(Date.now() / 1000))
}

/**
 * Reports whether the given access token is expired (or unreadable). The
 * payload's `exp` claim is a JWT numeric date in seconds. A null or malformed
 * token is treated as expired so the UI falls back to the login screen rather
 * than showing a broken state.
 */
export function isExpired(tokenToCheck: string | null = token): boolean {
  const payload = decodeTokenPayload(tokenToCheck)
  if (typeof payload?.exp !== 'number') return true
  return payload.exp <= Math.floor(Date.now() / 1000)
}

/**
 * Reports whether the user is currently authenticated: there is a token and it
 * is not expired. Used to decide between rendering the app and the login
 * screen.
 */
export function isAuthed(): boolean {
  return token !== null && !isExpired(token)
}

/** Authorization header for fetch requests, or an empty object when logged out. */
export function authHeaders(): Record<string, string> {
  return token ? { Authorization: `Bearer ${token}` } : {}
}

/**
 * Appends the token as an `access_token` query parameter. Required for the SSE
 * endpoint because the browser EventSource API cannot send custom headers.
 */
export function withAccessToken(url: string): string {
  if (!token) return url
  const sep = url.includes('?') ? '&' : '?'
  return `${url}${sep}access_token=${encodeURIComponent(token)}`
}

/** Exchanges credentials for a token and stores it on success. */
export async function login(username: string, password: string): Promise<void> {
  const res = await fetch('/api/v1/auth/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  })

  if (!res.ok) {
    let message = 'Login failed'
    if (res.status === 401) {
      message = 'Invalid username or password'
    } else {
      try {
        const body = (await res.json()) as { error?: string }
        if (body.error) message = body.error
      } catch {
        // ignore parse errors
      }
    }
    throw new Error(message)
  }

  const data = (await res.json()) as { token: string }
  setToken(data.token)
}

/**
 * Silently exchanges the HttpOnly refresh cookie for a new access token. Used
 * both proactively (before expiry) and reactively (after a 401). On success
 * the new access token is stored; on failure the token is cleared so the app
 * returns to the login screen.
 *
 * Concurrent callers (the proactive timer, the SSE reconnect handler, and the
 * 401 retry path) share a single in-flight refresh via refreshInFlight, so a
 * token expiry that triggers several refreshes at once results in exactly one
 * POST /api/v1/auth/refresh rather than a burst.
 */
let refreshInFlight: Promise<boolean> | null = null
export async function refresh(): Promise<boolean> {
  if (!refreshInFlight) {
    refreshInFlight = doRefresh().finally(() => {
      refreshInFlight = null
    })
  }
  return refreshInFlight
}

async function doRefresh(): Promise<boolean> {
  try {
    const res = await fetch('/api/v1/auth/refresh', {
      method: 'POST',
      // Send cookies (the refresh token) with the request.
      credentials: 'same-origin',
    })
    if (!res.ok) {
      // The server rejected the refresh token (invalid/expired) — the session
      // is genuinely over, so clear the access token and return to login.
      setToken(null)
      return false
    }
    const data = (await res.json()) as { token: string }
    setToken(data.token)
    return true
  } catch {
    // A network error (e.g. a brief server restart) does NOT mean the refresh
    // token is bad — the HttpOnly cookie is still valid. Keep the existing
    // access token so the app stays on screen and retries on the next call
    // instead of forcing a full re-login for a transient blip.
    return false
  }
}

/** Clears the stored token and notifies the server to expire its refresh cookie. */
export async function logout(): Promise<void> {
  try {
    await fetch('/api/v1/auth/logout', {
      method: 'POST',
      credentials: 'same-origin',
    })
  } catch {
    // best-effort; the cookie is cleared server-side regardless
  }
  setToken(null)
}
