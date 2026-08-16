// Helpers for normalising OCI tag lists in the UI.

// isSignatureTag reports whether a tag is a cosign signature or attestation
// artifact rather than a real release. These are pushed as sha256-<hex> subject
// tags and <hex>.sig / <hex>.att companion tags and must be hidden from users.
export function isSignatureTag(tag: string): boolean {
  if (tag.startsWith('sha256-')) return true
  return tag.endsWith('.sig') || tag.endsWith('.att')
}

// sortTagsDesc sorts tags semver-descending when parseable, otherwise
// lexicographically, so the newest-looking release appears first.
export function sortTagsDesc(tags: string[]): string[] {
  const semver = (t: string) => {
    const m = t.match(/^v?(\d+)\.(\d+)\.(\d+)/)
    return m ? [Number(m[1]), Number(m[2]), Number(m[3])] : null
  }
  return [...tags].sort((a, b) => {
    const sa = semver(a)
    const sb = semver(b)
    if (sa && sb) {
      for (let i = 0; i < 3; i++) {
        if (sb[i] !== sa[i]) return sb[i] - sa[i]
      }
    }
    return a.localeCompare(b)
  })
}

// cleanTags removes signature/attestation tags and sorts the remainder
// semver-descending. Use this on raw tag lists from the registry.
export function cleanTags(tags: string[]): string[] {
  return sortTagsDesc(tags.filter((t) => !isSignatureTag(t)))
}
