import type { Order } from '../../api/types'
import { ociName } from '../../utils/format'

export function orderSourceLabel(o: Order): string {
  if (o.menuRef) return `Menu ${o.menuRef.name}`
  if (!o.source) return '—'
  const where = o.source.pantryRef && !o.source.oci ? `Pantry ${o.source.pantryRef.name}` : o.source.oci ?? ''
  return o.source.version ? `${where} @ ${o.source.version}` : where
}

/** Short form for lists: artifact name and version only. */
export function orderSourceShort(o: Order): string {
  if (!o.source?.oci) return orderSourceLabel(o)
  const name = ociName(o.source.oci)
  return o.source.version ? `${name} @ ${o.source.version}` : name
}
