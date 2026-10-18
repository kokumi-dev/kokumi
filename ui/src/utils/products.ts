import type { AllowedPatchTarget, ImageFieldSpec, ImagePolicy, Order, OverridePolicy, Product } from '../api/types'
import { overridePolicy } from '../api/types'
import { stripOCIScheme } from './format'

/** Returns the Product an Order is a variant of, if it is loaded. */
export function productOf(products: Product[] | null, order: Order): Product | undefined {
  return products?.find((p) => p.namespace === order.namespace && p.name === order.productRef.name)
}

/** Where an Order pushes its Preparations: its destination URL or the default registry. */
export function orderDestinationLabel(order: Order): string {
  return order.destination?.url || `${order.effectiveDestination ?? ''} (default)`
}

/** Key that groups Orders under their Product. */
export function productKey(namespace: string, name: string): string {
  return `${namespace}/${name}`
}

/** Groups Orders by productKey, each group sorted by name. */
export function ordersByProduct(orders: Order[] | null): Map<string, Order[]> {
  const groups = new Map<string, Order[]>()
  for (const o of orders ?? []) {
    const key = productKey(o.namespace, o.productRef.name)
    groups.set(key, [...(groups.get(key) ?? []), o])
  }
  for (const list of groups.values()) list.sort((a, b) => a.name.localeCompare(b.name))
  return groups
}

/** Which Helm values Orders of the Product may set; None unless it renders with Helm. */
export function valuesPolicy(product: Product): OverridePolicy {
  return product.render?.helm ? overridePolicy(product.overrides?.values?.policy) : 'None'
}

/** Which patches and edits Orders of the Product may set. */
export function patchesPolicy(product: Product): OverridePolicy {
  return overridePolicy(product.overrides?.patches?.policy)
}

/** Whether Orders of the Product may set Helm values. */
export function allowsValues(product: Product): boolean {
  return valuesPolicy(product) !== 'None'
}

/** Whether Orders of the Product may set patches and edits. */
export function allowsPatches(product: Product): boolean {
  return patchesPolicy(product) !== 'None'
}

/** What a values policy lets Orders do, in words. */
export function describeValuesPolicy(policy: OverridePolicy): string {
  switch (policy) {
    case 'All':
      return 'Orders may set any Helm value'
    case 'Listed':
      return 'Orders may set only the listed value paths'
    default:
      return 'Orders may not set Helm values'
  }
}

/** What a patches policy lets Orders do, in words. */
export function describePatchesPolicy(policy: OverridePolicy): string {
  switch (policy) {
    case 'All':
      return 'Orders may patch and edit any resource'
    case 'Listed':
      return 'Orders may patch and edit only the listed fields'
    default:
      return 'Orders may not set patches or edits'
  }
}

/** What an image policy does, in words. */
export function describeImagePolicy(policy: ImagePolicy): string {
  switch (policy) {
    case 'Pin':
      return 'Every image reference is pinned to its digest; name and tag stay for readability'
    case 'Vendor':
      return "Every image is copied into a repository of its own below the Product's vendor location, under its name and tag, and pulled from there by digest"
    default:
      return 'Every image is listed by digest; the manifests stay unchanged'
  }
}

/** An image field spec in words, e.g. "Prometheus.monitoring.coreos.com: spec/image". */
export function imageFieldSpecLabel(f: ImageFieldSpec): string {
  const group = f.group ? `.${f.group}` : ''
  const version = f.version ? ` (${f.version})` : ''
  return `${f.kind}${group}${version}: ${f.path}`
}

/** An allowed patch target in words, e.g. "Deployment/web: .spec.replicas". */
export function allowedTargetLabel(a: AllowedPatchTarget): string {
  const where = a.target.namespace ? ` in ${a.target.namespace}` : ''
  return `${a.target.kind}/${a.target.name}${where}: ${a.paths.join(', ')}`
}

/** Why a Product takes no changes and no new Orders; undefined unless it is being deleted. */
export function productDeletingReason(product: Product): string | undefined {
  return product.deletionTimestamp ? 'This Product is being deleted' : undefined
}

/** Source with version, e.g. "oci://ghcr.io/org/app@1.2.3". */
export function productSourceLabel(p: Product): string {
  return p.source.version ? `${p.source.url}@${p.source.version}` : p.source.url
}

/** Source without version for lists, e.g. "ghcr.io/org/app". */
export function productSourceRef(p: Product): string {
  return p.source.url ? stripOCIScheme(p.source.url) : '—'
}
