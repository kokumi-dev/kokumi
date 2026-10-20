import { createContext, useContext } from 'react'
import type { Capability } from './api/capabilities'

/** Who is logged in, as whoami reports it. */
export interface Viewer {
  subject: string
  groups: string[]
}

export interface AppInfo {
  operatorName?: string
  operatorVersion?: string
  /** null while the whoami lookup is still in flight. */
  capabilities: ReadonlySet<Capability> | null
  /** null while the whoami lookup is still in flight. */
  viewer: Viewer | null
  onLogout: () => void
}

export const AppContext = createContext<AppInfo>({ capabilities: new Set(), viewer: null, onLogout: () => {} })

export function useAppInfo(): AppInfo {
  return useContext(AppContext)
}
