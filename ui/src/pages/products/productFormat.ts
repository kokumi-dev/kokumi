import { useOutletContext } from 'react-router'
import type { OCIDestination, Product } from '../../api/types'

/** Where a Product vendors its source to. */
export function vendorLabel(vendor: OCIDestination): string {
  if (vendor.pantryRef?.name) return `Pantry ${vendor.pantryRef.name}`
  return vendor.oci || 'In-cluster registry'
}

export function useProductContext(): { product: Product } {
  return useOutletContext<{ product: Product }>()
}
