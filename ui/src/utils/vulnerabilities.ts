import type {
  FindingSeverity,
  ImageReport,
  ImageUse,
  Preparation,
  PreparationVulnerabilityReport,
  Product,
  SBOMOrigin,
  VulnerabilityCheck,
  VulnerabilitySummary,
} from '../api/types'
import type { BadgeTone } from '../components/shared/Badge'
import { formatDuration, plural, shortDigest } from './format'
import { imageShortName } from './images'

/** An image as the Security tabs show it: its scan, or why it has none. */
export interface ScannedImage {
  /** Identifies the image in the table and in the ?image= query. */
  key: string
  /** How the table names the image; several when the manifests name it in several ways. */
  names: string[]
  /** A short name for titles, e.g. podinfo:6.14.1. */
  label: string
  /** The full name, shown on hover. */
  title: string
  /** The image's digest; the scan's when there is one. */
  digest?: string
  scan?: ImageReport
  /** The Orders whose Preparations run the image. */
  usedBy?: ImageUse[]
  /** Why the image has no scan. */
  missing?: string
  /** What to do about the missing scan. */
  missingHint?: string
  /** Findings the check blocks on; absent without a check. */
  blocking?: number
}

/** Which Preparations Kokumi keeps scanned, as the lists and the Security tab explain it. */
export const scannedPreparationsHint =
  'Kokumi keeps the three newest Ready Preparations of an Order, its target and the deployed one scanned.'

/** Where to read about a vulnerability: the CVE record, else its OSV entry (GHSA, GO, PYSEC, …). */
export function findingURL(id: string): string {
  return /^CVE-/i.test(id)
    ? `https://www.cve.org/CVERecord?id=${encodeURIComponent(id)}`
    : `https://osv.dev/vulnerability/${encodeURIComponent(id)}`
}

const severityOrder: readonly FindingSeverity[] = ['Unknown', 'Low', 'Medium', 'High', 'Critical']

/** Ranks a severity, higher is more severe; Unknown and anything unrecognized rank lowest. */
export function severityRank(severity: string): number {
  return Math.max(0, severityOrder.indexOf(severity as FindingSeverity))
}

/** The badge colour of a severity: Critical is danger, High a warning, the rest neutral. */
export function severityTone(severity: string): BadgeTone {
  switch (severity) {
    case 'Critical':
      return 'danger'
    case 'High':
      return 'warning'
    default:
      return 'neutral'
  }
}

/** The counts of a summary in words, e.g. "2 critical, 5 high, 0 medium, 1 low, 0 unknown; 6 fixable". */
export function summaryText(s: VulnerabilitySummary): string {
  return `${s.critical} critical, ${s.high} high, ${s.medium} medium, ${s.low} low, ${s.unknown} unknown; ${s.fixable} fixable`
}

/** How the UI names an SBOM origin. */
export function sbomOriginLabel(origin: SBOMOrigin): string {
  switch (origin) {
    case 'Image':
      return 'From image'
    default:
      return origin
  }
}

/** Where an SBOM of an origin came from, in words. */
export function describeSBOMOrigin(origin: SBOMOrigin): string {
  switch (origin) {
    case 'Generated':
      return 'Kokumi generated the SBOM from the image and attached it, signed'
    case 'Image':
      return 'BuildKit wrote the SBOM into the image index; the image digest covers it'
    case 'Signed':
      return 'Attached to the image and signed by a key the Kitchen trusts'
    default:
      return origin
  }
}

/** The last segment of an image name, e.g. podinfo:6.14.1. */
export function lastSegment(name: string): string {
  return name.split('/').pop() || name
}

/**
 * A reference as short as it stays readable: its last path segment with
 * the tag, e.g. podinfo:6.14.1, or with a short digest when it has no tag.
 */
export function shortRef(ref: string): string {
  const [name, digest] = ref.split('@')
  const short = lastSegment(name)
  if (!digest || short.includes(':')) return short
  return `${short}@${shortDigest(digest)}`
}

/** Shortens a name to max characters, e.g. a Preparation name with a hash. */
export function shortName(name: string, max = 22): string {
  return name.length > max ? `${name.slice(0, max - 1)}…` : name
}

/**
 * The summed scans of a Preparation's vendored images, from its Product's
 * record alone: null when one of them has no scan, undefined when none is
 * vendored or the Product is unknown.
 */
export function preparationSummary(prep: Preparation, product?: Product): VulnerabilitySummary | null | undefined {
  const vendored = (prep.images ?? []).filter((i) => i.vendored)
  if (vendored.length === 0 || !product) return undefined
  const records = new Map((product.vulnerabilities?.images ?? []).map((i) => [i.digest, i.summary]))
  const digests = new Set<string>()
  for (const image of vendored) {
    if (!image.digest || !records.has(image.digest)) return null
    digests.add(image.digest)
  }
  const sum: VulnerabilitySummary = { critical: 0, high: 0, medium: 0, low: 0, unknown: 0, fixable: 0 }
  for (const digest of digests) {
    const s = records.get(digest)!
    sum.critical += s.critical
    sum.high += s.high
    sum.medium += s.medium
    sum.low += s.low
    sum.unknown += s.unknown
    sum.fixable += s.fixable
  }
  return sum
}

/** Which findings a check blocks on, e.g. "Blocks High or higher findings". */
function blockedSeverities(check: VulnerabilityCheck): string {
  return `Blocks ${check.maxSeverity === 'Critical' ? 'Critical' : `${check.maxSeverity} or higher`} findings`
}

/** A vulnerability check in words, e.g. "Blocks High or higher findings · unfixed ignored · scans up to 2 days old". */
export function describeVulnerabilityCheck(check: VulnerabilityCheck): string {
  const age = check.maxAgeSeconds
    ? `scans up to ${formatDuration(check.maxAgeSeconds)} old`
    : 'scans up to twice the scanning interval old'
  return [blockedSeverities(check), check.ignoreUnfixed ? 'unfixed ignored' : undefined, age].filter(Boolean).join(' · ')
}

/** The first names, and how many more there are. */
function listNames(names: string[], max = 3): string {
  if (names.length <= max) return names.join(', ')
  return `${names.slice(0, max).join(', ')} and ${names.length - max} more`
}

/** Whether a Preparation passes its vulnerability check, as one line. */
export interface Verdict {
  tone: BadgeTone
  text: string
  /** False while the check holds the Preparation back. */
  passes: boolean
}

/**
 * The verdict of a Preparation's vulnerability check, in the order the
 * Serving controller decides it: images without a verified scan first,
 * then scans older than the check allows, then blocking findings. The age
 * is only known when the check sets maxAgeSeconds.
 */
export function checkVerdict(report: PreparationVulnerabilityReport, now: number = Date.now()): Verdict {
  const check = report.check
  if (!check) return { tone: 'neutral', text: 'No vulnerability check', passes: true }

  const images = report.images ?? []
  // A report that could not be read counts as missing, whatever blocking says.
  const missing = images.filter((i) => !i.scan || i.scan.error)
  if (missing.length > 0) {
    return { tone: 'warning', text: `Scan missing for ${listNames(missing.map((i) => imageShortName(i)))}`, passes: false }
  }
  const maxAge = check.maxAgeSeconds
  if (maxAge) {
    const stale = images.filter((i) => new Date(i.scan!.scannedTime).getTime() + maxAge * 1000 <= now)
    if (stale.length > 0) {
      return { tone: 'warning', text: `Scan older than ${formatDuration(maxAge)} for ${listNames(stale.map((i) => imageShortName(i)))}`, passes: false }
    }
  }
  const blocking = images.reduce((n, i) => n + i.blocking, 0)
  if (blocking > 0) {
    return {
      tone: 'danger',
      text: `${plural(blocking, 'finding')} at or above ${check.maxSeverity} ${blocking === 1 ? 'blocks' : 'block'} promotion`,
      passes: false,
    }
  }
  return { tone: 'success', text: 'Passes the vulnerability check', passes: true }
}
