import { act, renderHook } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { freshImport, json, stubFetch, tokenFor } from '../test/helpers'

/** An EventSource that a test opens, feeds and fails. */
class FakeEventSource extends EventTarget {
  static opened: FakeEventSource[] = []
  readonly url: string
  closed = false

  constructor(url: string) {
    super()
    this.url = url
    FakeEventSource.opened.push(this)
  }

  close(): void {
    this.closed = true
  }

  /** The access token the stream was opened with. */
  token(): string | null {
    return new URL(this.url, location.origin).searchParams.get('access_token')
  }

  emit(type: string, data: unknown): void {
    act(() => {
      this.dispatchEvent(new MessageEvent(type, { data: JSON.stringify(data) }))
    })
  }
}

const load = () =>
  freshImport(async () => ({ auth: await import('../api/auth'), sse: await import('./useSSEEvent') }))

let auth: Awaited<ReturnType<typeof load>>['auth']
let sse: Awaited<ReturnType<typeof load>>['sse']
let fetchMock: ReturnType<typeof stubFetch>

beforeEach(async () => {
  vi.useFakeTimers()
  FakeEventSource.opened = []
  vi.stubGlobal('EventSource', FakeEventSource)
  fetchMock = stubFetch()
  ;({ auth, sse } = await load())
  auth.setToken(tokenFor(300))
})

/** The streams that are open. */
const open = () => FakeEventSource.opened.filter((es) => !es.closed)

/** Lets ms pass, with the server's heartbeat every 15 s on the open stream. */
async function elapse(ms: number): Promise<void> {
  for (let left = ms; left > 0; left -= 15_000) {
    await act(() => vi.advanceTimersByTimeAsync(Math.min(15_000, left)))
    open().forEach((es) => es.emit('heartbeat', {}))
  }
}

/** Stores a renewed token, as a refresh does. */
function renew(): string {
  const token = tokenFor(300)
  act(() => auth.setToken(token))
  return token
}

const watchCounts = () => renderHook(() => sse.useSSEEvent<{ orders: number }>('counts'))

describe('useSSEEvent', () => {
  it('opens one stream with the access token and returns its latest value', () => {
    const { result } = watchCounts()
    renderHook(() => sse.useSSEEvent('orders'))
    expect(open()).toHaveLength(1)
    expect(open()[0].token()).toBe(auth.getToken())

    expect(result.current).toBeNull()
    open()[0].emit('counts', { orders: 2 })
    expect(result.current).toEqual({ orders: 2 })
  })

  it('keeps the stream open when the token is renewed', async () => {
    watchCounts()
    await elapse(60_000)
    renew()
    await elapse(60_000)
    renew()
    expect(FakeEventSource.opened).toHaveLength(1)
  })

  it('reopens the stream with the newest token 30 seconds before its token expires', async () => {
    const { result } = watchCounts()
    const first = open()[0]
    first.emit('counts', { orders: 1 })

    await elapse(60_000)
    renew()
    await elapse(180_000)
    const newest = renew()
    await elapse(29_000)
    expect(FakeEventSource.opened).toHaveLength(1)

    await elapse(1_000)
    expect(first.closed).toBe(true)
    expect(open()).toHaveLength(1)
    expect(open()[0].token()).toBe(newest)
    expect(result.current).toEqual({ orders: 1 })
  })

  it('reopens the stream with the next token when none is newer by then', async () => {
    watchCounts()
    await elapse(280_000)
    expect(FakeEventSource.opened).toHaveLength(1)

    const next = renew()
    expect(FakeEventSource.opened).toHaveLength(2)
    expect(open()[0].token()).toBe(next)
  })

  it('closes the stream at a sign-out and drops its values', () => {
    const { result } = watchCounts()
    const stream = open()[0]
    stream.emit('counts', { orders: 1 })

    act(() => auth.setToken(null))
    expect(stream.closed).toBe(true)
    expect(result.current).toBeNull()
  })

  it('renews the token and reconnects when the stream fails', async () => {
    watchCounts()
    const renewed = tokenFor(300)
    fetchMock.mockResolvedValueOnce(json(200, { token: renewed }))

    act(() => {
      open()[0].dispatchEvent(new Event('error'))
    })
    await act(() => vi.advanceTimersByTimeAsync(0))
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/auth/refresh', expect.anything())
    expect(open()).toHaveLength(1)
    expect(open()[0].token()).toBe(renewed)
  })
})
