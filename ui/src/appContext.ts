import { createContext, useContext } from 'react'
import type { Capability } from './api/capabilities'

export interface AppInfo {
  operatorName?: string
  operatorVersion?: string
  /** null while the whoami lookup is still in flight. */
  capabilities: ReadonlySet<Capability> | null
  onLogout: () => void
}

export const AppContext = createContext<AppInfo>({ capabilities: new Set(), onLogout: () => {} })

export function useAppInfo(): AppInfo {
  return useContext(AppContext)
}
