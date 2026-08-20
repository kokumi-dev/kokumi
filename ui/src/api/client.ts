import { authHeaders, refresh } from './auth'
import type { Order, Preparation, OrderFormData, Menu, MenuFormData, Patch, ChartInfo, Pantry, PantryFormData, ArtifactInfo, ArtifactFile } from './types'

// All API calls are relative so they work both in dev (proxied by Vite) and
// in production (served from the same Go binary).
const BASE = '/api/v1'

// ── Helpers ──────────────────────────────────────────────────────────────────

// refreshInFlight collapses concurrent 401s into a single refresh call so we
// don't fire a refresh storm when many requests fail at once after expiry.
let refreshInFlight: Promise<boolean> | null = null
function refreshOnce(): Promise<boolean> {
  if (!refreshInFlight) {
    refreshInFlight = refresh().finally(() => {
      refreshInFlight = null
    })
  }
  return refreshInFlight
}

// handleUnauthorized triggers a silent refresh on 401. If the refresh succeeds
// the caller should retry the original request; if it fails the token is
// cleared (via refresh()) and the app returns to the login screen.
async function handleUnauthorized(status: number): Promise<boolean> {
  if (status !== 401) return false
  return refreshOnce()
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    headers: { 'Content-Type': 'application/json', ...authHeaders(), ...init?.headers },
    ...init,
  })

  if (!res.ok) {
    const recovered = await handleUnauthorized(res.status)
    if (recovered) {
      // Retry once with the freshly issued access token.
      const retry = await fetch(`${BASE}${path}`, {
        headers: { 'Content-Type': 'application/json', ...authHeaders(), ...init?.headers },
        ...init,
      })
      if (retry.ok) {
        if (retry.status === 204) return undefined as T
        return retry.json() as Promise<T>
      }
    }
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

export function createOrder(data: OrderFormData, commitMessage?: string): Promise<Order> {
  // Edits are only set via the manifest editor on existing orders, never on create.
  return request<Order>('/orders', {
    method: 'POST',
    body: JSON.stringify({ ...data, edits: undefined, commitMessage: commitMessage ?? '' }),
  })
}

export function updateOrder(
  namespace: string,
  name: string,
  data: Omit<OrderFormData, 'name' | 'namespace'>,
  commitMessage?: string,
): Promise<Order> {
  return request<Order>(`/orders/${namespace}/${name}`, {
    method: 'PUT',
    body: JSON.stringify({ ...data, commitMessage: commitMessage ?? '' }),
  })
}

export function deleteOrder(namespace: string, name: string): Promise<void> {
  return request<void>(`/orders/${namespace}/${name}`, { method: 'DELETE' })
}

export function saveOrderEdits(
  namespace: string,
  name: string,
  edits: Patch[],
  commitMessage?: string,
): Promise<Order> {
  return request<Order>(`/orders/${namespace}/${name}/edits`, {
    method: 'PUT',
    body: JSON.stringify({ edits, commitMessage: commitMessage ?? '' }),
  })
}

export function getDefaultRegistry(): Promise<{ baseURL: string }> {
  return request<{ baseURL: string }>('/registry/default')
}

export function listOCITags(
  ref: string,
  pantryName?: string,
  namespace?: string,
): Promise<string[]> {
  let url = `/registry/tags?ref=${encodeURIComponent(ref)}`
  if (pantryName) {
    url += `&pantryName=${encodeURIComponent(pantryName)}`
    if (namespace) url += `&pantryNamespace=${encodeURIComponent(namespace)}`
  }
  return request<{ tags: string[] }>(url).then((r) => r.tags)
}

export function getArtifactInfo(
  ref: string,
  version: string,
  pantryName?: string,
  namespace?: string,
): Promise<ArtifactInfo> {
  let url = `/registry/artifact?ref=${encodeURIComponent(ref)}&version=${encodeURIComponent(version)}`
  if (pantryName) {
    url += `&pantryName=${encodeURIComponent(pantryName)}`
    if (namespace) url += `&pantryNamespace=${encodeURIComponent(namespace)}`
  }
  return request<ArtifactInfo>(url)
}

export function getChartInfo(ref: string, version: string, pantryName?: string, namespace?: string): Promise<ChartInfo> {
  let url = `/registry/chart-info?ref=${encodeURIComponent(ref)}&version=${encodeURIComponent(version)}`
  if (pantryName) {
    url += `&pantryName=${encodeURIComponent(pantryName)}`
    if (namespace) url += `&pantryNamespace=${encodeURIComponent(namespace)}`
  }
  return request<ChartInfo>(url)
}

// ── Preparations ──────────────────────────────────────────────────────────────

export function listPreparations(
  namespace: string,
  orderName: string,
): Promise<Preparation[]> {
  return request<Preparation[]>(`/orders/${namespace}/${orderName}/preparations`)
}

export function previewOrder(data: OrderFormData): Promise<string> {
  return fetch(`${BASE}/orders/preview`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders() },
    body: JSON.stringify(data),
  }).then(async (res) => {
    if (!res.ok) {
      handleUnauthorized(res.status)
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
  })
}

export function previewOrderFiles(data: OrderFormData): Promise<ArtifactFile[]> {
  return request<ArtifactFile[]>('/orders/preview/files', {
    method: 'POST',
    body: JSON.stringify(data),
  })
}

export function getManifest(
  namespace: string,
  prepName: string,
): Promise<string> {
  return fetch(`${BASE}/preparations/${namespace}/${prepName}/manifest`, {
    headers: { ...authHeaders() },
  }).then(
    async (res) => {
      if (!res.ok) {
        handleUnauthorized(res.status)
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

export function getManifestFiles(
  namespace: string,
  prepName: string,
): Promise<ArtifactFile[]> {
  return request<ArtifactFile[]>(
    `/preparations/${namespace}/${prepName}/manifest/files`,
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

// ── Menus ─────────────────────────────────────────────────────────────────────

export function listMenus(): Promise<Menu[]> {
  return request<Menu[]>('/menus')
}

export function getMenu(name: string): Promise<Menu> {
  return request<Menu>(`/menus/${name}`)
}

export function createMenu(data: MenuFormData): Promise<Menu> {
  return request<Menu>('/menus', {
    method: 'POST',
    body: JSON.stringify(data),
  })
}

export function updateMenu(
  name: string,
  data: Omit<MenuFormData, 'name'>,
): Promise<Menu> {
  return request<Menu>(`/menus/${name}`, {
    method: 'PUT',
    body: JSON.stringify(data),
  })
}

export function deleteMenu(name: string): Promise<void> {
  return request<void>(`/menus/${name}`, { method: 'DELETE' })
}

// ── Pantries ──────────────────────────────────────────────────────────────────

export function listPantries(): Promise<Pantry[]> {
  return request<Pantry[]>('/pantries')
}

export function getPantry(namespace: string, name: string): Promise<Pantry> {
  return request<Pantry>(`/pantries/${namespace}/${name}`)
}

export function createPantry(data: PantryFormData): Promise<Pantry> {
  return request<Pantry>('/pantries', {
    method: 'POST',
    body: JSON.stringify(data),
  })
}

export function updatePantry(
  namespace: string,
  name: string,
  data: Omit<PantryFormData, 'name' | 'namespace'>,
): Promise<Pantry> {
  return request<Pantry>(`/pantries/${namespace}/${name}`, {
    method: 'PUT',
    body: JSON.stringify(data),
  })
}

export function deletePantry(namespace: string, name: string): Promise<void> {
  return request<void>(`/pantries/${namespace}/${name}`, { method: 'DELETE' })
}

// ── Settings (singleton Kitchen) ──────────────────────────────────────────────

export interface Settings {
  argoCDURL: string
}

export function getSettings(): Promise<Settings> {
  return request<Settings>('/settings')
}

export function saveSettings(argoCDURL: string): Promise<Settings> {
  return request<Settings>('/settings', {
    method: 'PUT',
    body: JSON.stringify({ argoCDURL }),
  })
}
