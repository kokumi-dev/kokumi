import type { Product } from '../api/types'
import { useSSEEvent } from './useSSEEvent'

/** Subscribes to the `products` SSE event; null until the first event arrives. */
export function useProducts(): Product[] | null {
  return useSSEEvent<Product[]>('/api/v1/events', 'products')
}
