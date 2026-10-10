import { useOutletContext } from 'react-router'
import type { Order, Preparation } from '../../api/types'

export interface PreparationContext {
  prep: Preparation
  order?: Order
  /** Parent in the Order's history, if it still exists. */
  parent?: Preparation
  /** The currently active Preparation of the same Order. */
  active?: Preparation
  editsAllowed: boolean
}

export function usePreparationContext(): PreparationContext {
  return useOutletContext<PreparationContext>()
}
