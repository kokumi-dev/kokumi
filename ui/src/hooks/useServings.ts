import { useMemo } from 'react'
import type { DeploymentTarget, Serving } from '../api/types'
import { getServingTargets } from '../api/client'
import { useSSEEvent } from './useSSEEvent'
import { useThrottledFetch, type Fetched } from './useThrottledFetch'

/**
 * Subscribes to the `servings` SSE event and returns the full list of
 * Servings. Returns null until the first event is received.
 */
export function useServings(): Serving[] | null {
  return useSSEEvent<Serving[]>('servings')
}

/**
 * useServingTargets reads every cluster a Serving deploys to, with how its
 * deployment stands there; its status only counts them. It is read again,
 * throttled, when the Serving changes.
 */
export function useServingTargets(serving: Serving | undefined): Fetched<DeploymentTarget[]> {
  const namespace = serving?.namespace ?? ''
  const name = serving?.name ?? ''
  const version = useMemo(() => JSON.stringify(serving), [serving])
  return useThrottledFetch(serving ? `${namespace}/${name}` : '', version, () => getServingTargets(namespace, name))
}
