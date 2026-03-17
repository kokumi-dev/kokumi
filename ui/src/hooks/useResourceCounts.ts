import { useSSEEvent } from './useSSEEvent'

export interface ResourceCounts {
  orders: number
  preparations: number
  servings: number
}

/**
 * Returns the latest resource counts pushed from the server, or null until
 * the first SSE event is received.
 */
export function useResourceCounts(): ResourceCounts | null {
  return useSSEEvent<ResourceCounts>('/api/v1/events', 'counts')
}
