import type { Order, Preparation, OrderFormData } from './types'

// All API calls are relative so they work both in dev (proxied by Vite) and
// in production (served from the same Go binary).
const BASE = '/api/v1'

// ── Helpers ──────────────────────────────────────────────────────────────────

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    headers: { 'Content-Type': 'application/json', ...init?.headers },
    ...init,
  })

  if (!res.ok) {
    let message = `HTTP ${res.status}`
    try {
      const body = (await res.json()) as { error?: string }
      if (body.error) message = body.error
    } catch {
      // ignore parse errors
    }
    throw new Error(message)
  }

  // 204 No Content has no body.
  if (res.status === 204) return undefined as T
  return res.json() as Promise<T>
}

// ── Orders ────────────────────────────────────────────────────────────────────

export function listOrders(): Promise<Order[]> {
  return request<Order[]>('/orders')
}

export function getOrder(namespace: string, name: string): Promise<Order> {
  return request<Order>(`/orders/${namespace}/${name}`)
}

export function createOrder(data: OrderFormData): Promise<Order> {
  return request<Order>('/orders', {
    method: 'POST',
    body: JSON.stringify(data),
  })
}

export function updateOrder(
  namespace: string,
  name: string,
  data: Omit<OrderFormData, 'name' | 'namespace'>,
): Promise<Order> {
  return request<Order>(`/orders/${namespace}/${name}`, {
    method: 'PUT',
    body: JSON.stringify(data),
  })
}

export function deleteOrder(namespace: string, name: string): Promise<void> {
  return request<void>(`/orders/${namespace}/${name}`, { method: 'DELETE' })
}

// ── Preparations ──────────────────────────────────────────────────────────────

export function listPreparations(
  namespace: string,
  orderName: string,
): Promise<Preparation[]> {
  return request<Preparation[]>(`/orders/${namespace}/${orderName}/preparations`)
}

export function getManifest(
  namespace: string,
  prepName: string,
): Promise<string> {
  return fetch(`${BASE}/preparations/${namespace}/${prepName}/manifest`).then(
    async (res) => {
      if (!res.ok) {
        let message = `HTTP ${res.status}`
        try {
          const body = (await res.json()) as { error?: string }
          if (body.error) message = body.error
        } catch {
          // ignore
        }
        throw new Error(message)
      }
      return res.text()
    },
  )
}

// ── Promote ───────────────────────────────────────────────────────────────────

export function promote(
  namespace: string,
  orderName: string,
  preparation: string,
): Promise<{ serving: string }> {
  return request<{ serving: string }>(
    `/orders/${namespace}/${orderName}/promote`,
    {
      method: 'POST',
      body: JSON.stringify({ preparation }),
    },
  )
}
