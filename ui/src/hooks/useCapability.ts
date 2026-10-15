import { useEffect, useState } from 'react'
import { deniedReasons, type Capability, type NamespacedCapability } from '../api/capabilities'
import { getNamespaceCapabilities } from '../api/client'
import { useAppInfo } from '../appContext'

/** Reports whether the user has the capability; false while whoami loads. */
export function useCan(capability: Capability): boolean {
  const { capabilities } = useAppInfo()
  return capabilities?.has(capability) ?? false
}

/**
 * Returns why the user may not perform the action, or undefined when they
 * may. Meant for the disabledReason prop of Button and ButtonLink.
 */
export function useDeniedReason(capability: Capability): string | undefined {
  return useCan(capability) ? undefined : deniedReasons[capability]
}

/**
 * Reports whether the user has the namespaced capability in namespace: null
 * while unknown. The answer is fetched shortly after the namespace stops
 * changing, so typing a namespace does not query every keystroke.
 */
export function useNamespaceCan(namespace: string, capability: NamespacedCapability): boolean | null {
  const [answer, setAnswer] = useState<{ namespace: string; granted: boolean } | null>(null)

  useEffect(() => {
    if (!namespace) return
    let cancelled = false
    const timer = setTimeout(() => {
      getNamespaceCapabilities(namespace)
        .then((r) => {
          if (!cancelled) setAnswer({ namespace, granted: r.capabilities.includes(capability) })
        })
        .catch(() => {
          if (!cancelled) setAnswer(null)
        })
    }, 300)
    return () => {
      cancelled = true
      clearTimeout(timer)
    }
  }, [namespace, capability])

  return answer?.namespace === namespace ? answer.granted : null
}
