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

/** Formats a size in bytes, e.g. "512 B", "4.2 KB", "18 MB". */
export function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  const units = ['KB', 'MB', 'GB']
  let value = bytes / 1024
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit++
  }
  return `${value < 10 ? value.toFixed(1) : Math.round(value)} ${units[unit]}`
}

/** Formats a count with a singular/plural noun, e.g. "1 approval", "2 approvals". */
export function plural(n: number, noun: string): string {
  return `${n} ${noun}${n === 1 ? '' : 's'}`
}

/** Strips the oci:// scheme, e.g. for compact registry URLs in lists. */
export function stripOCIScheme(url: string): string {
  return url.replace(/^oci:\/\//, '')
}

const relativeUnits: [Intl.RelativeTimeFormatUnit, number][] = [
  ['year', 365 * 24 * 3600],
  ['month', 30 * 24 * 3600],
  ['week', 7 * 24 * 3600],
  ['day', 24 * 3600],
  ['hour', 3600],
  ['minute', 60],
]

/** Formats an ISO date relative to now, e.g. "3 hours ago". Returns '—' for empty input. */
export function formatRelative(iso?: string, now: number = Date.now()): string {
  if (!iso) return '—'
  const seconds = Math.round((new Date(iso).getTime() - now) / 1000)
  const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto', style: 'short' })
  for (const [unit, size] of relativeUnits) {
    if (Math.abs(seconds) >= size) return rtf.format(Math.round(seconds / size), unit)
  }
  return rtf.format(0, 'second')
}

/** Formats a duration in seconds, e.g. "7 days", "36 hours", "1.5 hours", "30 minutes". */
export function formatDuration(seconds: number): string {
  const day = 24 * 3600
  if (seconds >= day && seconds % day === 0) return plural(seconds / day, 'day')
  if (seconds >= 3600) return plural(Math.round((seconds / 3600) * 10) / 10, 'hour')
  return plural(Math.round(seconds / 60), 'minute')
}
