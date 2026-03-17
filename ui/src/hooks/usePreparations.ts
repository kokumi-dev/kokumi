import { useMemo } from 'react'
import type { Preparation } from '../api/types'
import { useSSEEvent } from './useSSEEvent'

/**
 * Subscribes to the `preparations` SSE event. When `orderName` is provided
 * the list is filtered client-side to only include Preparations for that
 * Order. Returns null until the first event is received.
 */
export function usePreparations(orderName?: string): Preparation[] | null {
  const all = useSSEEvent<Preparation[]>('/api/v1/events', 'preparations')

  return useMemo(() => {
    if (all === null) return null
    if (!orderName) return all
    return all.filter((p) => p.order === orderName)
  }, [all, orderName])
}
