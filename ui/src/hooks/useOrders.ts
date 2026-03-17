import type { Order } from '../api/types'
import { useSSEEvent } from './useSSEEvent'

/**
 * Subscribes to the `orders` SSE event and returns the live list of all
 * Orders enriched with their active Preparation name. Returns null until the
 * first event is received (i.e. the cache has synced after server start).
 */
export function useOrders(): Order[] | null {
  return useSSEEvent<Order[]>('/api/v1/events', 'orders')
}
