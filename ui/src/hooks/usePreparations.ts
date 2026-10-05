import type { Preparation } from '../api/types'
import { useSSEEvent } from './useSSEEvent'

/**
 * Subscribes to the `preparations` SSE event. Returns null until the first
 * event is received.
 */
export function usePreparations(): Preparation[] | null {
  return useSSEEvent<Preparation[]>('/api/v1/events', 'preparations')
}
