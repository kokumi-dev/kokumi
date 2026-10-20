import { useOutletContext } from 'react-router'
import type { Order, Preparation, Product, Serving } from '../../api/types'

export interface OrderContext {
  order: Order
  /** The Product the Order is a variant of; undefined until loaded or when it is missing. */
  product?: Product
  /** False when the Order's Product forbids patches (or has not loaded yet). */
  editsAllowed: boolean
  /** Newest first; null until loaded. */
  preparations: Preparation[] | null
  servings: Serving[] | null
  /** Every Order and Serving, for the Order's pipeline; null until loaded. */
  allOrders: Order[] | null
  allServings: Serving[] | null
}

export function useOrderContext(): OrderContext {
  return useOutletContext<OrderContext>()
}
