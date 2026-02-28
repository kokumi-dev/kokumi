import type { Recipe } from '../api/types'
import { useSSEEvent } from './useSSEEvent'

/**
 * Subscribes to the `recipes` SSE event and returns the live list of all
 * Recipes enriched with their active Preparation name. Returns null until the
 * first event is received (i.e. the cache has synced after server start).
 */
export function useRecipes(): Recipe[] | null {
  return useSSEEvent<Recipe[]>('/api/v1/events', 'recipes')
}
