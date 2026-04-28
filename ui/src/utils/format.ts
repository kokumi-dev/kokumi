/**
 * Formats an ISO date string into a short locale-aware date+time string.
 * Returns '—' for undefined/null input.
 */
export function formatDate(iso?: string): string {
  if (!iso) return '—'
  return new Date(iso).toLocaleString(undefined, {
    dateStyle: 'short',
    timeStyle: 'short',
  })
}

/**
 * Returns the first 8 hex characters of a digest, stripping the "sha256:" prefix.
 * Used to render compact digest chips in tables and lists.
 */
export function shortDigest(digest: string): string {
  return digest.replace('sha256:', '').slice(0, 8)
}

/**
 * Maps a Kubernetes resource phase string to a CSS module class key.
 * Used by pages that render inline phase badges without the shared Badge component.
 */
export function phaseToStatusKey(phase: string): 'badgeSuccess' | 'badgeError' | 'badgeWarning' {
  const p = phase.toLowerCase()
  if (p === 'ready' || p === 'succeeded' || p === 'deployed') return 'badgeSuccess'
  if (p === 'failed' || p === 'error') return 'badgeError'
  return 'badgeWarning'
}
