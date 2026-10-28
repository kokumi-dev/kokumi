import { useEffect, useState } from 'react'
import { getSettings } from '../api/client'

/** Returns the validated Argo CD base URL from the Kitchen settings, or ''. */
export function useArgoCDBase(): string {
  const [raw, setRaw] = useState('')

  useEffect(() => {
    getSettings()
      .then((s) => setRaw(s.argoCDURL))
      .catch(() => {})
  }, [])

  const trimmed = raw.trim().replace(/\/$/, '')
  if (!trimmed) return ''
  try {
    const u = new URL(trimmed)
    return u.protocol === 'http:' || u.protocol === 'https:' ? trimmed : ''
  } catch {
    return ''
  }
}

/** The page of an Argo CD Application, which Kokumi creates in Argo CD's namespace argocd; null without a base URL. */
export function argoAppURL(base: string, applicationName: string): string | null {
  return base ? `${base}/applications/argocd/${encodeURIComponent(applicationName)}` : null
}
