import type { Environment } from '../api/types'
import { useSSEEvent } from './useSSEEvent'

/**
 * Subscribes to the `environments` SSE event and returns the live list of
 * the Environments of every namespace. Returns null until the first event is
 * received, which only users who may list Environments everywhere get.
 */
export function useEnvironments(): Environment[] | null {
  return useSSEEvent<Environment[]>('environments')
}
