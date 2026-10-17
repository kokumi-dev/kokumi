import type { PreparationImage } from '../api/types'

const indexMediaTypes = new Set([
  'application/vnd.oci.image.index.v1+json',
  'application/vnd.docker.distribution.manifest.list.v2+json',
])

/** Whether the image is an index of several platforms. */
export function isMultiPlatform(image: PreparationImage): boolean {
  return !!image.mediaType && indexMediaTypes.has(image.mediaType)
}

/** The reference that pulls exactly this image: the vendored copy, or the name at its digest. */
export function pinnedReference(image: PreparationImage): string | undefined {
  if (image.vendored) return image.vendored
  if (image.name && image.digest) return `${image.name}@${image.digest}`
  return undefined
}

/** How one image reference differs between two Preparations. */
export interface ImageChange {
  ref: string
  kind: 'added' | 'removed' | 'changed'
  before?: PreparationImage
  after?: PreparationImage
}

/**
 * The image references that were added, removed, or resolved to another
 * digest or location between two Preparations, sorted by reference. A tag
 * that moved upstream shows up here even when the manifests are unchanged.
 */
export function diffImages(before: PreparationImage[] = [], after: PreparationImage[] = []): ImageChange[] {
  const old = new Map(before.map((i) => [i.ref, i]))
  const now = new Map(after.map((i) => [i.ref, i]))
  const changes: ImageChange[] = []
  for (const ref of [...new Set([...old.keys(), ...now.keys()])].sort()) {
    const b = old.get(ref)
    const a = now.get(ref)
    if (!b) changes.push({ ref, kind: 'added', after: a })
    else if (!a) changes.push({ ref, kind: 'removed', before: b })
    else if (b.digest !== a.digest || b.vendored !== a.vendored) changes.push({ ref, kind: 'changed', before: b, after: a })
  }
  return changes
}
