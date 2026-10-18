import { useOutletContext } from 'react-router'
import type { OCIDestination, Product, SourceVerification } from '../../api/types'
import { keyLabel } from '../../hooks/useSigningKeys'

/** Where a Product vendors its source to. */
export function vendorLabel(vendor: OCIDestination): string {
  return vendor.url || 'In-cluster registry'
}

/** Describes the signer whose signature on a source verified. */
export function signerLabel(keys: ReadonlyMap<string, string>, verified: SourceVerification): string {
  if (verified.keyID) return `Signed by ${keyLabel(keys, verified.keyID)}`
  return `Signed by ${verified.subject} (issuer ${verified.issuer})`
}

export function useProductContext(): { product: Product } {
  return useOutletContext<{ product: Product }>()
}
