import { beforeEach, describe, expect, it, vi } from 'vitest'
import { deferred, freshImport, installLocks, jwt, json, stubFetch, tokenFor } from '../test/helpers'

const SIGN_OUT_KEY = 'kokumi.signOut'
const LAST_REPORT_KEY = 'kokumi.lastActivityReport'
const REFRESH = '/api/v1/auth/refresh'
const LOGOUT = '/api/v1/auth/logout'

const loadAuth = () => freshImport(() => import('./auth'))

let auth: Awaited<ReturnType<typeof loadAuth>>
let fetchMock: ReturnType<typeof stubFetch>

beforeEach(async () => {
  vi.useFakeTimers()
  fetchMock = stubFetch()
  auth = await loadAuth()
})

/** The paths of the requests sent so far. */
const paths = () => fetchMock.mock.calls.map(([url]) => String(url))

/** The body of each refresh request sent so far. */
const refreshBodies = () =>
  fetchMock.mock.calls.filter(([url]) => url === REFRESH).map(([, init]) => JSON.parse(String(init?.body)))

/** User input in this tab. */
const input = (type = 'pointerdown') => window.dispatchEvent(new Event(type))

/** Another tab writes key in localStorage, which this tab sees as a storage event. */
function otherTabWrites(key: string, value: string): void {
  localStorage.setItem(key, value)
  window.dispatchEvent(new StorageEvent('storage', { key, newValue: value }))
}

/** Lets the promises settle that are waiting for nothing but each other. */
const settle = () => vi.advanceTimersByTimeAsync(0)

describe('refresh', () => {
  it('stores the token of a successful refresh', async () => {
    const next = tokenFor(300)
    fetchMock.mockResolvedValueOnce(json(200, { token: next }))
    const changed = vi.fn()
    auth.onAuthChange(changed)

    await expect(auth.refresh()).resolves.toBe(true)
    expect(auth.getToken()).toBe(next)
    expect(changed).toHaveBeenCalledOnce()
    expect(fetchMock).toHaveBeenCalledWith(REFRESH, expect.objectContaining({ method: 'POST', credentials: 'same-origin' }))
  })

  it('keeps the token while the server cannot check the session', async () => {
    const held = tokenFor(300)
    auth.setToken(held)

    fetchMock.mockResolvedValueOnce(json(503, { error: 'the session cannot be checked at the OIDC provider' }))
    await expect(auth.refresh()).resolves.toBe(false)
    expect(auth.getToken()).toBe(held)

    fetchMock.mockRejectedValueOnce(new TypeError('Failed to fetch'))
    await expect(auth.refresh()).resolves.toBe(false)
    expect(auth.getToken()).toBe(held)
  })

  it.each([400, 401])('clears the token when the server ends the session with %i', async (status) => {
    auth.setToken(tokenFor(300))
    fetchMock.mockResolvedValueOnce(json(status, { error: 'invalid or expired refresh token' }))

    await expect(auth.refresh()).resolves.toBe(false)
    expect(auth.getToken()).toBeNull()
  })

  it('sends one request for refreshes that overlap', async () => {
    const reply = deferred<Response>()
    fetchMock.mockReturnValueOnce(reply.promise)

    const refreshes = [auth.refresh(), auth.refresh(), auth.refresh()]
    reply.resolve(json(200, { token: tokenFor(300) }))
    await expect(Promise.all(refreshes)).resolves.toEqual([true, true, true])
    expect(fetchMock).toHaveBeenCalledOnce()
  })

  it('takes turns with other tabs, so that no two send the same refresh cookie', async () => {
    const locks = installLocks()
    const otherTab = await loadAuth()
    const first = deferred<Response>()
    fetchMock.mockReturnValueOnce(first.promise).mockResolvedValueOnce(json(200, { token: tokenFor(300) }))

    const mine = auth.refresh()
    const theirs = otherTab.refresh()
    await settle()
    expect(fetchMock).toHaveBeenCalledOnce()

    first.resolve(json(200, { token: tokenFor(300) }))
    await expect(mine).resolves.toBe(true)
    await expect(theirs).resolves.toBe(true)
    expect(fetchMock).toHaveBeenCalledTimes(2)
    expect(locks.requested).toEqual(['kokumi.refresh', 'kokumi.refresh'])
  })
})

describe('activity', () => {
  beforeEach(() => {
    auth.setToken(tokenFor(300))
    fetchMock.mockImplementation(async () => json(200, { token: tokenFor(300) }))
  })

  it('reports input after a minute without a report at once', async () => {
    input()
    await settle()
    expect(refreshBodies()).toEqual([{ active: true }])
  })

  it('reports activity at most once a minute', async () => {
    input()
    await settle()
    await vi.advanceTimersByTimeAsync(59_000)
    input('keydown')
    await settle()
    expect(refreshBodies()).toHaveLength(1)

    await vi.advanceTimersByTimeAsync(2_000)
    input('wheel')
    await settle()
    expect(refreshBodies()).toEqual([{ active: true }, { active: true }])
  })

  it('tries a failed report again only after a minute', async () => {
    fetchMock.mockImplementation(async () => json(503, { error: 'unavailable' }))
    input()
    await settle()
    input()
    await settle()
    expect(refreshBodies()).toHaveLength(1)

    await vi.advanceTimersByTimeAsync(61_000)
    input()
    await settle()
    expect(refreshBodies()).toHaveLength(2)
  })

  it('tells a background renewal from activity', async () => {
    input()
    await settle()
    await vi.advanceTimersByTimeAsync(61_000)
    await auth.refresh()
    expect(refreshBodies()).toEqual([{ active: true }, { active: false }])
  })

  it('counts a report of another tab', async () => {
    otherTabWrites(LAST_REPORT_KEY, String(Date.now()))
    input()
    await settle()
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('counts input in another tab as activity', async () => {
    otherTabWrites('kokumi.lastInput', String(Date.now()))
    await auth.refresh()
    expect(refreshBodies()).toEqual([{ active: true }])
  })

  it('does not read storage on input', () => {
    otherTabWrites(LAST_REPORT_KEY, String(Date.now()))
    const getItem = vi.spyOn(Storage.prototype, 'getItem')
    for (let i = 0; i < 10; i++) input('pointermove')
    expect(getItem).not.toHaveBeenCalled()
  })

  it('starts the window at a sign-in', async () => {
    auth.setToken(null)
    fetchMock.mockResolvedValueOnce(json(200, { token: tokenFor(300) }))
    await auth.login('admin', 'secret')
    input()
    await settle()
    expect(paths()).toEqual(['/api/v1/auth/login'])
  })
})

describe('sign-out', () => {
  it('signs out every tab', () => {
    auth.setToken(tokenFor(300))
    otherTabWrites(SIGN_OUT_KEY, String(Date.now()))
    expect(auth.getToken()).toBeNull()
  })

  it('ends the session at the server', async () => {
    auth.setToken(tokenFor(300))
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 204 }))

    await auth.logout()
    expect(auth.getToken()).toBeNull()
    expect(paths()).toEqual([LOGOUT])
    expect(localStorage.getItem(SIGN_OUT_KEY)).toBeNull()
  })

  it('goes on to the end-session URL of the provider', async () => {
    auth.setToken(tokenFor(300))
    // A same-document URL, the only navigation jsdom performs.
    fetchMock.mockResolvedValueOnce(json(200, { endSessionURL: `${location.origin}/#signed-out` }))

    await auth.logout()
    expect(location.hash).toBe('#signed-out')
  })

  it('keeps a sign-out the server did not confirm, and the next refresh sends it instead of resuming the session', async () => {
    auth.setToken(tokenFor(300))
    fetchMock.mockRejectedValueOnce(new TypeError('Failed to fetch'))
    await auth.logout()
    expect(localStorage.getItem(SIGN_OUT_KEY)).not.toBeNull()

    // A reload, or another tab, tries to resume the session from the cookie.
    const reloaded = await loadAuth()
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 204 }))
    await expect(reloaded.refresh()).resolves.toBe(false)
    expect(paths()).toEqual([LOGOUT, LOGOUT])
    expect(reloaded.getToken()).toBeNull()
    expect(localStorage.getItem(SIGN_OUT_KEY)).toBeNull()
  })

  it('sends the sign-out after a refresh on its way, and the refreshed token does not resume the session', async () => {
    auth.setToken(tokenFor(300))
    const renewal = deferred<Response>()
    fetchMock.mockReturnValueOnce(renewal.promise).mockResolvedValueOnce(new Response(null, { status: 204 }))

    const refreshing = auth.refresh()
    const signingOut = auth.logout()
    await settle()
    expect(paths()).toEqual([REFRESH])

    renewal.resolve(json(200, { token: tokenFor(300) }))
    await expect(refreshing).resolves.toBe(false)
    await signingOut
    expect(paths()).toEqual([REFRESH, LOGOUT])
    expect(auth.getToken()).toBeNull()
  })
})

describe('access token', () => {
  it('reads the claims of a token whose payload uses the base64url alphabet', () => {
    // Three '?' in a row encode to '_' at one of them.
    const token = tokenFor(300, { sub: 'ops???team' })
    expect(token.split('.')[1]).toMatch(/[-_]/)
    auth.setToken(token)
    expect(auth.isAuthed()).toBe(true)
    expect(auth.getUsername()).toBe('ops???team')
  })

  it('reads a payload as UTF-8', () => {
    auth.setToken(tokenFor(300, { email: 'jürgen@example.com' }))
    expect(auth.getUsername()).toBe('jürgen@example.com')
  })

  it('takes the remaining lifetime from the exp claim', () => {
    auth.setToken(tokenFor(300))
    expect(auth.getTokenTTL()).toBe(300)
  })

  it('treats a missing, unreadable or expired token as expired', () => {
    expect(auth.isExpired(null)).toBe(true)
    expect(auth.isExpired('not-a-jwt')).toBe(true)
    expect(auth.isExpired(jwt({ sub: 'user' }))).toBe(true)
    expect(auth.isExpired(tokenFor(0))).toBe(true)
    expect(auth.isExpired(tokenFor(1))).toBe(false)
  })

  it('takes the token of an SSO sign-in from the URL fragment and removes it from the URL', () => {
    localStorage.setItem(SIGN_OUT_KEY, '1')
    const token = tokenFor(300)
    history.replaceState(null, '', `/orders?tab=all#access_token=${encodeURIComponent(token)}`)

    expect(auth.consumeFragmentToken()).toBe(true)
    expect(auth.getToken()).toBe(token)
    expect(`${location.pathname}${location.search}${location.hash}`).toBe('/orders?tab=all')
    expect(localStorage.getItem(SIGN_OUT_KEY)).toBeNull()
    expect(localStorage.getItem(LAST_REPORT_KEY)).toBe(String(Date.now()))
  })
})
