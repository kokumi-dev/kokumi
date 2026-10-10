import { useOutletContext } from 'react-router'
import type { Menu } from '../../api/types'
import { ociName } from '../../utils/format'

export function menuSourceLabel(m: Menu): string {
  const where = m.source.pantryRef?.name ? `Pantry ${m.source.pantryRef.name}` : m.source.oci ?? ''
  return m.source.version ? `${where} @ ${m.source.version}` : where
}

/** Short form for lists: artifact name and version only. */
export function menuSourceShort(m: Menu): string {
  if (!m.source.oci || m.source.pantryRef?.name) return menuSourceLabel(m)
  const name = ociName(m.source.oci)
  return m.source.version ? `${name} @ ${m.source.version}` : name
}

export function useMenuContext(): { menu: Menu } {
  return useOutletContext<{ menu: Menu }>()
}
