import yaml from 'js-yaml'
import type { Patch, PatchTarget } from '../api/types'

const DOC_SEPARATOR = /(?:^|\n)---[ \t]*(?:\n|$)/

interface K8sResource {
  kind?: string
  metadata?: { name?: string; namespace?: string }
  [key: string]: unknown
}

/**
 * Splits a multi-document YAML string into individual trimmed documents.
 */
function splitDocuments(manifest: string): string[] {
  return manifest
    .split(DOC_SEPARATOR)
    .map((d) => d.trim())
    .filter((d) => d.length > 0)
}

/**
 * Extracts the PatchTarget (kind, name, namespace) from a parsed YAML document.
 */
function identifyTarget(doc: unknown): PatchTarget | null {
  if (!doc || typeof doc !== 'object') return null
  const d = doc as K8sResource
  if (!d.kind || !d.metadata?.name) return null
  return {
    kind: d.kind,
    name: d.metadata.name,
    ...(d.metadata.namespace ? { namespace: d.metadata.namespace } : {}),
  }
}

function targetKey(t: PatchTarget): string {
  return `${t.kind}/${t.name}${t.namespace ? `/${t.namespace}` : ''}`
}

/**
 * Recursively walks two objects and collects changed scalar values as
 * dot-separated jsonPaths (e.g. ".spec.replicas").
 */
function findChangedScalars(
  original: unknown,
  edited: unknown,
  path: string,
  result: Record<string, string>,
): void {
  if (edited === null || edited === undefined) return

  if (typeof edited !== 'object') {
    // Scalar — compare as string since patch values are always strings
    if (original === null || original === undefined || String(original) !== String(edited)) {
      result[path] = String(edited)
    }
    return
  }

  if (Array.isArray(edited)) {
    // Compare array elements by index
    const origArr = Array.isArray(original) ? original : []
    for (let i = 0; i < (edited as unknown[]).length; i++) {
      findChangedScalars(origArr[i], (edited as unknown[])[i], `${path}[${i}]`, result)
    }
    return
  }

  const origObj =
    original && typeof original === 'object' && !Array.isArray(original)
      ? (original as Record<string, unknown>)
      : {}

  for (const key of Object.keys(edited as Record<string, unknown>)) {
    findChangedScalars(origObj[key], (edited as Record<string, unknown>)[key], `${path}.${key}`, result)
  }
}

/**
 * Computes structured Patch edits from the diff between an original rendered
 * manifest and the user's edited version. Merges with any existing edits so
 * that previously saved edits are preserved unless overridden.
 */
export function computeEdits(
  originalManifest: string,
  editedManifest: string,
  existingEdits: Patch[],
): Patch[] {
  const origDocs = splitDocuments(originalManifest).map((d) => yaml.load(d))
  const editedDocs = splitDocuments(editedManifest).map((d) => yaml.load(d))

  // Index original docs by target key
  const origMap = new Map<string, unknown>()
  const targetMap = new Map<string, PatchTarget>()
  for (const doc of origDocs) {
    const target = identifyTarget(doc)
    if (target) {
      const key = targetKey(target)
      origMap.set(key, doc)
      targetMap.set(key, target)
    }
  }

  // Start with existing edits indexed by target key → (path → value)
  const editMap = new Map<string, { target: PatchTarget; set: Map<string, string> }>()
  for (const edit of existingEdits) {
    const key = targetKey(edit.target)
    editMap.set(key, {
      target: edit.target,
      set: new Map(Object.entries(edit.set)),
    })
  }

  // Find changes in each edited document
  for (const editedDoc of editedDocs) {
    const target = identifyTarget(editedDoc)
    if (!target) continue

    const key = targetKey(target)
    const origDoc = origMap.get(key) ?? {}
    targetMap.set(key, target)

    const changes: Record<string, string> = {}
    findChangedScalars(origDoc, editedDoc, '', changes)

    if (Object.keys(changes).length > 0) {
      const existing = editMap.get(key)
      const merged = existing?.set ?? new Map<string, string>()
      for (const [path, value] of Object.entries(changes)) {
        merged.set(path, value)
      }
      editMap.set(key, { target, set: merged })
    }
  }

  // Convert back to Patch[]
  const result: Patch[] = []
  for (const entry of editMap.values()) {
    if (entry.set.size > 0) {
      result.push({
        target: entry.target,
        set: Object.fromEntries(entry.set),
      })
    }
  }
  return result
}

/**
 * Creates a new `updateOrder`-compatible edits payload by directly providing
 * the edits array. This is used when saving from the manifest editor.
 */
export function patchEditsOnly(
  edits: Patch[],
): { edits: Patch[] } {
  return { edits }
}
