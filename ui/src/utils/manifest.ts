const CRD_PATTERN = /^kind:\s+CustomResourceDefinition\s*$/m
const DOC_SEPARATOR = /(?:^|\n)---[ \t]*(?:\n|$)/

/**
 * Splits a multi-document YAML string into individual documents.
 * Handles both leading `---` markers (common in Helm output) and
 * `\n---\n` separators used by the server when concatenating files.
 */
function splitDocuments(manifest: string): string[] {
  return manifest
    .split(DOC_SEPARATOR)
    .map((d) => d.trim())
    .filter((d) => d.length > 0)
}

/**
 * Returns true if the manifest contains at least one CRD document.
 */
export function hasCRDDocuments(manifest: string): boolean {
  return splitDocuments(manifest).some((doc) => CRD_PATTERN.test(doc))
}

/**
 * Filters CRD documents from a multi-document YAML manifest string.
 *
 * When `hideCRDs` is true, documents whose `kind` field is
 * `CustomResourceDefinition` are removed. Otherwise the manifest is
 * returned unchanged.
 */
export function filterCRDDocuments(manifest: string, hideCRDs: boolean): string {
  if (!hideCRDs) return manifest

  const docs = splitDocuments(manifest)
  const filtered = docs.filter((doc) => !CRD_PATTERN.test(doc))

  if (filtered.length === 0) return ''

  return filtered.join('\n---\n')
}
