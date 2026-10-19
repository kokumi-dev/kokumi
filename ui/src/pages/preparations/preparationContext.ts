import { useOutletContext } from 'react-router'
import type { Order, Preparation, Product } from '../../api/types'

export interface PreparationContext {
  prep: Preparation
  order?: Order
  /** The Product of the Order; undefined until loaded or when it is missing. */
  product?: Product
  /** Parent in the Order's history, if it still exists. */
  parent?: Preparation
  /** The currently active Preparation of the same Order. */
  active?: Preparation
  editsAllowed: boolean
}

export function usePreparationContext(): PreparationContext {
  return useOutletContext<PreparationContext>()
}
