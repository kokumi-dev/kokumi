import { useMemo } from 'react'
import type { Preparation } from '../api/types'
import { useSSEEvent } from './useSSEEvent'

/**
 * Subscribes to the `preparations` SSE event. When `recipeName` is provided
 * the list is filtered client-side to only include Preparations for that
 * Recipe. Returns null until the first event is received.
 */
export function usePreparations(recipeName?: string): Preparation[] | null {
  const all = useSSEEvent<Preparation[]>('/api/v1/events', 'preparations')

  return useMemo(() => {
    if (all === null) return null
    if (!recipeName) return all
    return all.filter((p) => p.recipe === recipeName)
  }, [all, recipeName])
}
