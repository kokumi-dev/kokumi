import type { Pantry } from '../api/types'
import { useSSEEvent } from './useSSEEvent'

/**
 * Subscribes to the `pantries` SSE event and returns the live list of all
 * Pantries. Returns null until the first event is received.
 */
export function usePantries(): Pantry[] | null {
  return useSSEEvent<Pantry[]>('/api/v1/events', 'pantries')
}
