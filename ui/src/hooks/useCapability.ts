import { deniedReasons, type Capability } from '../api/capabilities'
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
