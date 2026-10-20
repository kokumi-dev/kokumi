import { useOutletContext } from 'react-router'
import type { Serving } from '../../api/types'

export interface ServingContext {
  serving: Serving
}

export function useServingContext(): ServingContext {
  return useOutletContext<ServingContext>()
}
