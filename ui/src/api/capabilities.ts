/**
 * Capabilities are the UI actions the signed-in user may perform, reported by
 * GET /api/v1/whoami and named "<area>:<action>". They are display hints only:
 * the server enforces RBAC on every request. Keep in sync with
 * internal/server/capabilities.go.
 */
export const capabilities = [
  'settings.general:update',
  'settings.auth:update',
  'settings.signing:update',
  'orders:create',
  'orders:update',
  'orders:delete',
  'products:create',
  'products:update',
  'products:delete',
  'registries:create',
  'registries:update',
  'registries:delete',
  'repositories:create',
  'repositories:update',
  'repositories:delete',
  'preparations:promote',
  'preparations:approve',
] as const

export type Capability = typeof capabilities[number]

/** Tooltip on actions the user may not perform. */
export const deniedReasons: Record<Capability, string> = {
  'settings.general:update': 'You are not allowed to change the general settings',
  'settings.auth:update': 'You are not allowed to change the authentication settings',
  'settings.signing:update': 'You are not allowed to change the signing settings',
  'orders:create': 'You are not allowed to create Orders',
  'orders:update': 'You are not allowed to edit Orders',
  'orders:delete': 'You are not allowed to delete Orders',
  'products:create': 'You are not allowed to create Products',
  'products:update': 'You are not allowed to edit Products',
  'products:delete': 'You are not allowed to delete Products',
  'registries:create': 'You are not allowed to create Registries',
  'registries:update': 'You are not allowed to edit Registries',
  'registries:delete': 'You are not allowed to delete Registries',
  'repositories:create': 'You are not allowed to create Repositories',
  'repositories:update': 'You are not allowed to edit Repositories',
  'repositories:delete': 'You are not allowed to delete Repositories',
  'preparations:promote': 'You are not allowed to promote Preparations',
  'preparations:approve': 'Approving requires a personal SSO login with approval permission',
}

/**
 * Applies a whoami answer to the previous capabilities, ignoring names from a
 * newer server. Capabilities the server could not determine keep their
 * previous value, so a failing check does not revoke them; with no previous
 * value (first load) they are denied.
 */
export function mergeCapabilities(
  prev: ReadonlySet<Capability> | null,
  names: readonly string[],
  undetermined: readonly string[] = [],
): ReadonlySet<Capability> {
  return new Set(
    capabilities.filter((c) => names.includes(c) || (undetermined.includes(c) && (prev?.has(c) ?? false))),
  )
}

/**
 * Capabilities that hold per namespace, reported by
 * GET /api/v1/namespaces/{namespace}/capabilities. Keep in sync with
 * namespacedCapabilityCatalog in internal/server/capabilities.go.
 */
export const namespacedCapabilities = [
  'registries.credentials:update',
] as const

export type NamespacedCapability = typeof namespacedCapabilities[number]
