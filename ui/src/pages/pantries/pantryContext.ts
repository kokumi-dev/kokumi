import { useOutletContext } from 'react-router'
import type { Pantry } from '../../api/types'

export function usePantryContext(): { pantry: Pantry } {
  return useOutletContext<{ pantry: Pantry }>()
}
