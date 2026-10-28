import { useOutletContext } from 'react-router'
import type { DeploymentTarget, Serving } from '../../api/types'
import type { Fetched } from '../../hooks/useThrottledFetch'

export interface ServingContext {
  serving: Serving
  /** Every cluster it deploys to, as the server works them out. */
  targets: Fetched<DeploymentTarget[]>
  /** The Argo CD base URL; empty while unknown or unset. */
  argoBase: string
}

export function useServingContext(): ServingContext {
  return useOutletContext<ServingContext>()
}
