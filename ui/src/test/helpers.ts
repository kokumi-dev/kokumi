import { vi } from 'vitest'

// Event listeners added to window and document by modules that freshImport
// loaded, removed after each test (see setup.ts).
const added: Array<() => void> = []

/**
 * Imports modules anew, with module state of their own, as a new tab would.
 * The event listeners they add while loading are removed after the test, so
 * a module loaded for one test does not react to the next one.
 */
export async function freshImport<T>(load: () => Promise<T>): Promise<T> {
  vi.resetModules()
  const spies = [window, document].map((target) => {
    const add = target.addEventListener.bind(target)
    return vi.spyOn(target, 'addEventListener').mockImplementation((type, listener, options) => {
      add(type, listener, options)
      if (listener) added.push(() => target.removeEventListener(type, listener, options))
    })
  })
  try {
    return await load()
  } finally {
    spies.forEach((spy) => spy.mockRestore())
  }
}

/** Removes the event listeners of the modules freshImport loaded. */
export function removeImportedListeners(): void {
  added.splice(0).forEach((remove) => remove())
}

function base64url(text: string): string {
  let binary = ''
  new TextEncoder().encode(text).forEach((byte) => (binary += String.fromCharCode(byte)))
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

/** An unsigned JWT with claims; the UI reads only the payload. */
export function jwt(claims: Record<string, unknown>): string {
  return `${base64url(JSON.stringify({ alg: 'HS256', typ: 'JWT' }))}.${base64url(JSON.stringify(claims))}.signature`
}

let issued = 0

/** A new access token that expires in seconds, each one different. */
export function tokenFor(seconds: number, claims: Record<string, unknown> = {}): string {
  issued++
  return jwt({ sub: 'user', jti: String(issued), exp: Math.floor(Date.now() / 1000) + seconds, ...claims })
}

/** Replaces fetch with a mock for the test. */
export function stubFetch() {
  const fetchMock = vi.fn<typeof fetch>()
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

/** A response with a JSON body. */
export function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

/** A promise and the functions that settle it. */
export function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

/**
 * The Web Locks API, which jsdom lacks, for one origin: requests for a lock
 * run one after another, as across the tabs of a browser.
 */
export class FakeLockManager {
  readonly requested: string[] = []
  private queue: Promise<unknown> = Promise.resolve()

  request<T>(name: string, callback: () => Promise<T>): Promise<T> {
    this.requested.push(name)
    const run = this.queue.then(callback)
    this.queue = run.catch(() => undefined)
    return run
  }
}

/** Installs a FakeLockManager as navigator.locks for the test. */
export function installLocks(): FakeLockManager {
  const locks = new FakeLockManager()
  Object.defineProperty(navigator, 'locks', { value: locks, configurable: true })
  return locks
}
