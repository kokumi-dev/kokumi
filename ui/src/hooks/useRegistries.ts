import type { Registry, Repository } from '../api/types'
import { useSSEEvent } from './useSSEEvent'

/**
 * Subscribes to the `registries` SSE event and returns the live list of all
 * Registries. Returns null until the first event is received.
 */
export function useRegistries(): Registry[] | null {
  return useSSEEvent<Registry[]>('registries')
}

/**
 * Subscribes to the `repositories` SSE event and returns the live list of all
 * Repositories. Returns null until the first event is received.
 */
export function useRepositories(): Repository[] | null {
  return useSSEEvent<Repository[]>('repositories')
}
