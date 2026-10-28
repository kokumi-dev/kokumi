import { useOutletContext } from 'react-router'
import type { Cluster, Environment, Order } from '../../api/types'

export interface EnvironmentContext {
  environment: Environment
  /** The Clusters of its namespace, each with the Environments that select it; null while unknown. */
  clusters: Cluster[] | null
  /** The Orders of its namespace that deploy to it; null until loaded. */
  orders: Order[] | null
  /** The Environments of its namespace that require running after it. */
  requiredBy: Environment[]
}

export function useEnvironmentContext(): EnvironmentContext {
  return useOutletContext<EnvironmentContext>()
}
