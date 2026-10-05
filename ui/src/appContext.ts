import { createContext, useContext } from 'react'

export interface AppInfo {
  operatorName?: string
  operatorVersion?: string
  /** null while the whoami lookup is still in flight. */
  isAdmin: boolean | null
  onLogout: () => void
}

export const AppContext = createContext<AppInfo>({ isAdmin: false, onLogout: () => {} })

export function useAppInfo(): AppInfo {
  return useContext(AppContext)
}
