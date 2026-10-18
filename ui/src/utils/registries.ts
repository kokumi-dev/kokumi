import type { Registry, RegistryRef } from '../api/types'

/** Docker Hub's host names, which all name one registry. */
const dockerHubHosts = new Set(['docker.io', 'index.docker.io', 'registry-1.docker.io'])

interface Location {
  host: string
  path: string
}

/**
 * Parses an oci:// URL, or a reference without a scheme, into its host and
 * repository path; a tag or digest is dropped. Mirrors ParseLocation in
 * internal/registry.
 */
function parseLocation(url: string): Location | null {
  let rest = url.replace(/^oci:\/\//, '')
  rest = rest.split('@')[0]
  const slash = rest.indexOf('/')
  let host = (slash < 0 ? rest : rest.slice(0, slash)).toLowerCase()
  let path = slash < 0 ? '' : rest.slice(slash + 1)
  if (!host) return null
  const colon = path.lastIndexOf(':')
  if (colon >= 0 && !path.slice(colon).includes('/')) path = path.slice(0, colon)
  if (dockerHubHosts.has(host)) host = 'docker.io'
  return { host, path: path.replace(/^\/+|\/+$/g, '') }
}

function covers(prefix: Location, target: Location): boolean {
  if (prefix.host !== target.host) return false
  return prefix.path === '' || target.path === prefix.path || target.path.startsWith(`${prefix.path}/`)
}

/**
 * The Registry among registries whose url covers url with the longest path,
 * as the controller picks it. A hint for forms only: the controller decides.
 */
export function matchRegistry(url: string, registries: readonly Registry[]): Registry | undefined {
  const target = parseLocation(url)
  if (!target) return undefined
  let best: Registry | undefined
  let bestPath = -1
  for (const r of registries) {
    const loc = parseLocation(r.url)
    if (loc && covers(loc, target) && loc.path.length > bestPath) {
      best = r
      bestPath = loc.path.length
    }
  }
  return best
}

/** The Registries of namespace that refs name; names without a Registry are left out. */
export function referencedRegistries(
  registries: readonly Registry[] | null,
  namespace: string,
  refs: readonly RegistryRef[] | undefined,
): Registry[] {
  const names = new Set((refs ?? []).map((r) => r.name))
  return (registries ?? []).filter((r) => r.namespace === namespace && names.has(r.name))
}

/** The names refs lists, for read scopes. */
export function registryRefNames(refs: readonly RegistryRef[] | undefined): string[] {
  return (refs ?? []).map((r) => r.name)
}
