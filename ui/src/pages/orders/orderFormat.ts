import type { Order } from '../../api/types'
import { stripOCIScheme } from '../../utils/format'

export function orderSourceLabel(o: Order): string {
  if (o.menuRef) return `Menu ${o.menuRef.name}`
  if (!o.source) return '—'
  const where = o.source.pantryRef && !o.source.oci ? `Pantry ${o.source.pantryRef.name}` : o.source.oci ?? ''
  return o.source.version ? `${where}@${o.source.version}` : where
}

/** Source without version for lists, e.g. "ghcr.io/org/app". */
export function orderSourceRef(o: Order): string {
  if (o.menuRef) return `Menu ${o.menuRef.name}`
  if (o.source?.oci) return stripOCIScheme(o.source.oci)
  if (o.source?.pantryRef) return `Pantry ${o.source.pantryRef.name}`
  return '—'
}
