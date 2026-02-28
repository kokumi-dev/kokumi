import type { Recipe, Preparation, RecipeFormData } from './types'

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

// ── Recipes ───────────────────────────────────────────────────────────────────

export function listRecipes(): Promise<Recipe[]> {
  return request<Recipe[]>('/recipes')
}

export function getRecipe(namespace: string, name: string): Promise<Recipe> {
  return request<Recipe>(`/recipes/${namespace}/${name}`)
}

export function createRecipe(data: RecipeFormData): Promise<Recipe> {
  return request<Recipe>('/recipes', {
    method: 'POST',
    body: JSON.stringify(data),
  })
}

export function updateRecipe(
  namespace: string,
  name: string,
  data: Omit<RecipeFormData, 'name' | 'namespace'>,
): Promise<Recipe> {
  return request<Recipe>(`/recipes/${namespace}/${name}`, {
    method: 'PUT',
    body: JSON.stringify(data),
  })
}

export function deleteRecipe(namespace: string, name: string): Promise<void> {
  return request<void>(`/recipes/${namespace}/${name}`, { method: 'DELETE' })
}

// ── Preparations ──────────────────────────────────────────────────────────────

export function listPreparations(
  namespace: string,
  recipeName: string,
): Promise<Preparation[]> {
  return request<Preparation[]>(`/recipes/${namespace}/${recipeName}/preparations`)
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
  recipeName: string,
  preparation: string,
): Promise<{ serving: string }> {
  return request<{ serving: string }>(
    `/recipes/${namespace}/${recipeName}/promote`,
    {
      method: 'POST',
      body: JSON.stringify({ preparation }),
    },
  )
}
