import { load, dump } from 'js-yaml'
import type { Patch, RegistryRef } from '../api/types'

/**
 * Serialises a plain JS object to a YAML string.
 * Returns an empty string for null/empty objects.
 */
export function objectToYaml(values: Record<string, unknown>): string {
  if (!values || Object.keys(values).length === 0) return ''
  return dump(values, { lineWidth: 100 }).trimEnd()
}

/**
 * Parses a YAML string into a plain JS object (mapping).
 * Returns {} for empty/null input.
 * Throws if the input is not a mapping.
 */
export function yamlToValues(text: string): Record<string, unknown> {
  if (!text.trim()) return {}
  const parsed = load(text)
  if (parsed == null) return {}
  if (typeof parsed !== 'object' || Array.isArray(parsed))
    throw new Error('Values must be a YAML mapping')
  return parsed as Record<string, unknown>
}

/** Throws when obj has keys outside allowed; path prefixes the reported names. */
export function assertKnownKeys(obj: Record<string, unknown> | undefined, allowed: string[], path: string) {
  if (!obj) return
  const unknown = Object.keys(obj).filter((k) => !allowed.includes(k))
  if (unknown.length > 0) {
    throw new Error(`unknown field ${unknown.map((k) => (path ? `${path}.${k}` : k)).join(', ')}`)
  }
}

/** Patches as written in a YAML document, without empty namespaces. */
export function patchesToYaml(patches: Patch[]): unknown[] {
  return patches.map((p) => ({
    target: {
      kind: p.target.kind,
      name: p.target.name,
      ...(p.target.namespace ? { namespace: p.target.namespace } : {}),
    },
    set: p.set,
  }))
}

/** Reads the patches list of a parsed YAML document; anything but a list yields none. */
export function patchesFromYaml(raw: unknown): Patch[] {
  if (!Array.isArray(raw)) return []
  return raw.map((p) => {
    const patch = (p ?? {}) as Record<string, unknown>
    const target = (patch.target ?? {}) as Record<string, string>
    const set = (patch.set ?? {}) as Record<string, string>
    return {
      target: { kind: target.kind ?? '', name: target.name ?? '', namespace: target.namespace },
      set,
    }
  })
}

/** Reads a registryRefs list of a parsed YAML document; it must be a list of {name}. */
export function registryRefsFromYaml(raw: unknown): RegistryRef[] {
  if (raw === undefined || raw === null) return []
  if (!Array.isArray(raw)) throw new Error('registryRefs must be a list')
  return raw.map((r, i) => {
    const ref = (r ?? {}) as Record<string, unknown>
    assertKnownKeys(ref, ['name'], `registryRefs[${i}]`)
    if (typeof ref.name !== 'string' || !ref.name) throw new Error(`registryRefs[${i}].name is required`)
    return { name: ref.name }
  })
}
