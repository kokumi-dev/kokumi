import { useOutletContext } from 'react-router'
import type { Menu, Order, Preparation, Serving } from '../../api/types'

export interface OrderContext {
  order: Order
  menu?: Menu
  /** False when the Order's Menu forbids patches (or has not loaded yet). */
  editsAllowed: boolean
  /** Newest first; null until loaded. */
  preparations: Preparation[] | null
  servings: Serving[] | null
}

export function useOrderContext(): OrderContext {
  return useOutletContext<OrderContext>()
}
