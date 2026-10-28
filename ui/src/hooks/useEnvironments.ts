import { useMemo } from 'react'
import type { Cluster, ClusterDeployment, Environment } from '../api/types'
import { getClusterDeployments } from '../api/client'
import { useServings } from './useServings'
import { useSSEEvent } from './useSSEEvent'
import { useThrottledFetch, type Fetched } from './useThrottledFetch'

/**
 * Subscribes to the `environments` SSE event and returns the live list of
 * the Environments of every namespace. Returns null until the first event is
 * received, which only users who may list Environments everywhere get.
 */
export function useEnvironments(): Environment[] | null {
  return useSSEEvent<Environment[]>('environments')
}

/**
 * Subscribes to the `clusters` SSE event and returns the live list of the
 * Clusters of every namespace. Returns null until the first event is
 * received, which only users who may list Clusters everywhere get.
 */
export function useClusters(): Cluster[] | null {
  return useSSEEvent<Cluster[]>('clusters')
}

/**
 * useClusterDeployments reads what each Order deploys to the Cluster name of
 * namespace, which the server works out per cluster from the Argo CD
 * Applications. It is read again, throttled, when the Cluster or a Serving
 * of its namespace changes.
 */
export function useClusterDeployments(namespace: string, name: string): Fetched<ClusterDeployment[]> {
  const clusters = useClusters()
  const servings = useServings()
  const version = useMemo(
    () =>
      JSON.stringify([
        clusters?.find((c) => c.namespace === namespace && c.name === name),
        servings?.filter((s) => s.namespace === namespace),
      ]),
    [clusters, servings, namespace, name],
  )
  return useThrottledFetch(`${namespace}/${name}`, version, () => getClusterDeployments(namespace, name))
}
