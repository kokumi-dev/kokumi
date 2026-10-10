import { useOutletContext } from 'react-router'
import type { Menu } from '../../api/types'
import { stripOCIScheme } from '../../utils/format'

export function menuSourceLabel(m: Menu): string {
  const where = m.source.pantryRef?.name ? `Pantry ${m.source.pantryRef.name}` : m.source.oci ?? ''
  return m.source.version ? `${where}@${m.source.version}` : where
}

/** Source without version for lists, e.g. "ghcr.io/org/app". */
export function menuSourceRef(m: Menu): string {
  if (m.source.pantryRef?.name) return `Pantry ${m.source.pantryRef.name}`
  return m.source.oci ? stripOCIScheme(m.source.oci) : '—'
}

export function useMenuContext(): { menu: Menu } {
  return useOutletContext<{ menu: Menu }>()
}
