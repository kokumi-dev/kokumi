import type { ReactNode } from 'react'
import { Navigate } from 'react-router'
import type { Capability } from '../../api/capabilities'
import { useAppInfo } from '../../appContext'

/** Renders children when the user has any of the capabilities, else redirects to the dashboard. */
export default function RequireCapability({ anyOf, children }: { anyOf: Capability[]; children: ReactNode }) {
  const { capabilities } = useAppInfo()
  if (capabilities === null) return null
  return anyOf.some((c) => capabilities.has(c)) ? children : <Navigate to="/" replace />
}
