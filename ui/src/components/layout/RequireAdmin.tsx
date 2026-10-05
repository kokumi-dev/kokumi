import type { ReactNode } from 'react'
import { Navigate } from 'react-router'
import { useAppInfo } from '../../appContext'

export default function RequireAdmin({ children }: { children: ReactNode }) {
  const { isAdmin } = useAppInfo()
  if (isAdmin === null) return null
  return isAdmin ? children : <Navigate to="/" replace />
}
