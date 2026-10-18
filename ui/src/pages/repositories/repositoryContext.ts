import { useOutletContext } from 'react-router'
import type { Repository } from '../../api/types'

export function useRepositoryContext(): { repository: Repository } {
  return useOutletContext<{ repository: Repository }>()
}
