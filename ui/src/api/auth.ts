// Lightweight auth state for the UI. The token is kept in sessionStorage so it
// survives reloads within a tab but is cleared when the tab closes. Subscribers
// are notified whenever the token changes (login / logout / 401).

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

/** Clears the stored token. */
export function logout(): void {
  setToken(null)
}
