// Lightweight auth state for the UI. The access token is kept in sessionStorage
// so it survives reloads within a tab but is cleared when the tab closes. The
// refresh token is held by the server in an HttpOnly cookie and is never
// exposed to JS. Subscribers are notified whenever the token changes
// (login / logout / 401 / silent refresh).

const TOKEN_KEY = 'kokumi.token'

// SIGN_OUT_KEY is set in localStorage from a sign-out until the server has
// expired the refresh cookie. Every tab sees it change and signs out too, and
// while it is set no tab resumes the session from the cookie.
const SIGN_OUT_KEY = 'kokumi.signOut'

// The server ends a session after a time without user activity (its idle
// limit). A refresh reports whether the user was active, which moves that
// deadline; background renewals of an open but unattended tab do not.
//
// LAST_INPUT_KEY holds the time of the last user input in any tab, written at
// most every INPUT_WRITE_INTERVAL_MS. LAST_REPORT_KEY holds when a refresh in
// any tab last reported activity. Input within ACTIVE_WINDOW_MS counts as
// activity, and input after ACTIVE_WINDOW_MS without a report refreshes at
// once, so a user who comes back to a tab moves the deadline before it passes.
// The window bounds how far the deadline can trail the last input, at the cost
// of at most one refresh per window while the user is active.
const LAST_INPUT_KEY = 'kokumi.lastInput'
const LAST_REPORT_KEY = 'kokumi.lastActivityReport'
const INPUT_WRITE_INTERVAL_MS = 15_000
const ACTIVE_WINDOW_MS = 60_000

// REFRESH_LOCK serializes refreshes across tabs, so two tabs never redeem the
// same refresh token; the waiting tab sends the cookies the first one received.
const REFRESH_LOCK = 'kokumi.refresh'

let token: string | null = sessionStorage.getItem(TOKEN_KEY)
const listeners = new Set<() => void>()
let lastInput = 0
let lastInputWrite = 0
let lastReportAttempt = 0

// A sign-out in another tab ends the session in this one too.
window.addEventListener('storage', (event) => {
  if (event.key === SIGN_OUT_KEY && event.newValue !== null) setToken(null)
})

function readTime(key: string): number {
  try {
    return Number(localStorage.getItem(key)) || 0
  } catch {
    return 0
  }
}

function writeTime(key: string, time: number): void {
  try {
    localStorage.setItem(key, String(time))
  } catch {
    // Storage may be unavailable; activity then counts in this tab only.
  }
}

/** The time of the last user input in any tab. */
function lastInputAt(): number {
  return Math.max(lastInput, readTime(LAST_INPUT_KEY))
}

function recordInput(): void {
  const now = Date.now()
  lastInput = now
  if (now - lastInputWrite >= INPUT_WRITE_INTERVAL_MS) {
    lastInputWrite = now
    writeTime(LAST_INPUT_KEY, now)
  }
  // Report a return to an idle session at once, before its deadline passes;
  // at most once per window, also when the report fails.
  const lastReport = Math.max(readTime(LAST_REPORT_KEY), lastReportAttempt)
  if (token !== null && now - lastReport > ACTIVE_WINDOW_MS) {
    lastReportAttempt = now
    void refresh()
  }
}

for (const type of ['pointerdown', 'pointermove', 'keydown', 'wheel', 'touchstart']) {
  window.addEventListener(type, recordInput, { capture: true, passive: true })
}
document.addEventListener('visibilitychange', () => {
  if (document.visibilityState === 'visible') recordInput()
})

function signOutPending(): boolean {
  return localStorage.getItem(SIGN_OUT_KEY) !== null
}

/** Stores the token of a new sign-in, which ends a pending sign-out. */
function startSession(next: string): void {
  localStorage.removeItem(SIGN_OUT_KEY)
  // A sign-in starts the idle deadline, as a report of activity does.
  writeTime(LAST_REPORT_KEY, Date.now())
  setToken(next)
}

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
// missing or unreadable. Used to derive expiry and the username without a
// server-provided TTL.
function decodeTokenPayload(tokenToCheck: string | null = token): {
  exp?: number
  sub?: string
  email?: string
} | null {
  if (!tokenToCheck) return null
  const parts = tokenToCheck.split('.')
  if (parts.length < 2) return null
  try {
    return JSON.parse(atob(parts[1])) as { exp?: number; sub?: string; email?: string }
  } catch {
    return null
  }
}

/**
 * Returns the username of the current token, or null when there is no token
 * or it is unreadable. Shown in the sidebar. Prefers the email claim and falls
 *  back to the subject claim
 */
export function getUsername(): string | null {
  const payload = decodeTokenPayload()
  return payload?.email || payload?.sub || null
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
  startSession(data.token)
}

/**
 * Silently exchanges the HttpOnly refresh cookie for a new access token. Used
 * both proactively (before expiry) and reactively (after a 401). On success
 * the new access token is stored. When the server ends the session the token
 * is cleared so the app returns to the login screen; a network error or a 5xx,
 * such as an OIDC provider that cannot be reached, keeps it for a retry.
 *
 * Concurrent callers (the proactive timer, the SSE reconnect handler, and the
 * 401 retry path) share a single in-flight refresh via refreshInFlight, so a
 * token expiry that triggers several refreshes at once results in exactly one
 * POST /api/v1/auth/refresh rather than a burst.
 *
 * After a sign-out no session is resumed: while the server has not confirmed
 * the sign-out, a refresh retries it instead, e.g. on the next page load.
 */
let refreshInFlight: Promise<boolean> | null = null
export async function refresh(): Promise<boolean> {
  if (signOutPending()) {
    await finishSignOut()
    return false
  }
  if (!refreshInFlight) {
    refreshInFlight = doRefresh().finally(() => {
      refreshInFlight = null
    })
  }
  return refreshInFlight
}

async function doRefresh(): Promise<boolean> {
  // Missing outside a secure context; tabs then refresh on their own.
  const locks: LockManager | undefined = navigator.locks
  return locks ? locks.request(REFRESH_LOCK, exchangeRefreshToken) : exchangeRefreshToken()
}

async function exchangeRefreshToken(): Promise<boolean> {
  if (signOutPending()) return false
  const sentAt = Date.now()
  const active = sentAt - lastInputAt() < ACTIVE_WINDOW_MS
  try {
    const res = await fetch('/api/v1/auth/refresh', {
      method: 'POST',
      // Send cookies (the refresh token) with the request.
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ active }),
    })
    // The server or the OIDC provider cannot be reached right now; the
    // session may well be valid, so keep the token and retry later.
    if (res.status >= 500) return false
    if (!res.ok) {
      // The server ended the session (signed out, idle, expired, or refused
      // by the OIDC provider), so clear the access token and return to login.
      setToken(null)
      return false
    }
    const data = (await res.json()) as { token: string }
    // A sign-out started while this refresh was on its way: the new token
    // must not resume the session.
    if (signOutPending()) return false
    if (active) writeTime(LAST_REPORT_KEY, sentAt)
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

/**
 * Signs out: ends the session in every tab at once, then has the server
 * expire the refresh cookie. For an OIDC session the server may answer with
 * the provider's end-session URL, and the browser goes there to sign out of
 * the provider too. A sign-out the server does not confirm stays pending and
 * is retried by the next refresh, so a reload does not resume the session.
 */
export async function logout(): Promise<void> {
  localStorage.setItem(SIGN_OUT_KEY, String(Date.now()))
  setToken(null)
  // A refresh on its way may still set a new refresh cookie. Waiting for it
  // lets the sign-out response expire the cookie last.
  await refreshInFlight
  await finishSignOut()
}

/** Asks the server to expire the refresh cookie and follows its end-session URL, if any. */
async function finishSignOut(): Promise<void> {
  let res: Response
  try {
    res = await fetch('/api/v1/auth/logout', {
      method: 'POST',
      credentials: 'same-origin',
    })
  } catch {
    return // the server cannot be reached; the sign-out stays pending
  }
  if (!res.ok) return
  localStorage.removeItem(SIGN_OUT_KEY)
  if (res.status !== 200) return
  const data = (await res.json().catch(() => ({}))) as { endSessionURL?: string }
  if (data.endSessionURL) window.location.assign(data.endSessionURL)
}

/**
 * Reads an access token from the URL fragment (`#access_token=...`), stores it,
 * and strips the fragment so it is not leaked via referrers/history. The OIDC
 * callback uses the fragment because it is never sent to the server.
 */
export function consumeFragmentToken(): boolean {
  if (typeof window === 'undefined') return false
  const hash = window.location.hash
  if (!hash.includes('access_token=')) return false
  const params = new URLSearchParams(hash.slice(1))
  const t = params.get('access_token')
  if (t) {
    startSession(t)
    // Strip the fragment so the token is not persisted in history or sent on later navigations.
    history.replaceState(null, '', window.location.pathname + window.location.search)
    return true
  }
  return false
}
