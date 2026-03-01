import type { Serving } from '../api/types'
import { useSSEEvent } from './useSSEEvent'

/**
 * Subscribes to the `servings` SSE event and returns the full list of
 * Servings. Returns null until the first event is received.
 */
export function useServings(): Serving[] | null {
  return useSSEEvent<Serving[]>('/api/v1/events', 'servings')
}
