import { useOutletContext } from 'react-router'
import type { Cluster } from '../../api/types'

export interface ClusterContext {
  cluster: Cluster
  /** The Argo CD base URL; empty while unknown or unset. */
  argoBase: string
}

export function useClusterContext(): ClusterContext {
  return useOutletContext<ClusterContext>()
}
