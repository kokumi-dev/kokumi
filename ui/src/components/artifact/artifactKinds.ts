import type { ArtifactGraph, ArtifactNode, Verification } from '../../api/types'
import type { Status } from '../graph/parts'

/** The artifactType of a referrer that carries a Sigstore bundle, as cosign v3 writes it. */
const sigstoreBundleType = 'application/vnd.dev.sigstore.bundle.v0.3+json'

/** The predicate type of the statement `cosign sign` signs. */
const signaturePredicate = 'https://sigstore.dev/cosign/sign/v1'

/** The icon a kind of node is drawn with. */
export type IconKind = 'artifact' | 'signature' | 'attestation' | 'approvals' | 'sbom' | 'index' | 'unknown'

export interface Kind {
  label: string
  icon: IconKind
}

/** Names of the statements Kokumi and cosign sign. Only verified bundles get one. */
const knownPredicates: Record<string, Kind> = {
  [signaturePredicate]: { label: 'Signature', icon: 'signature' },
  'https://slsa.dev/provenance/v1': { label: 'SLSA provenance', icon: 'attestation' },
  'https://kokumi.dev/attestations/approval/v1': { label: 'Approvals', icon: 'approvals' },
  'https://kokumi.dev/attestations/promotion/v1': { label: 'Promotion record', icon: 'attestation' },
  'https://kokumi.dev/attestations/deployment/v1': { label: 'Deployment record', icon: 'attestation' },
  'https://spdx.dev/Document': { label: 'SBOM attestation', icon: 'sbom' },
  'https://cyclonedx.org/bom': { label: 'SBOM attestation', icon: 'sbom' },
}

/** Names of artifactTypes, or of the config media type of manifests without one. */
const knownArtifactTypes: Record<string, Kind> = {
  'application/vnd.kokumi.preparation.v1': { label: 'Preparation', icon: 'artifact' },
  'application/vnd.cncf.helm.config.v1+json': { label: 'Helm chart', icon: 'artifact' },
  'application/spdx+json': { label: 'SBOM', icon: 'sbom' },
  'text/spdx': { label: 'SBOM', icon: 'sbom' },
  'application/vnd.cyclonedx+json': { label: 'SBOM', icon: 'sbom' },
  'application/vnd.cyclonedx': { label: 'SBOM', icon: 'sbom' },
  'application/vnd.oci.image.config.v1+json': { label: 'Container image', icon: 'artifact' },
  'application/vnd.docker.container.image.v1+json': { label: 'Container image', icon: 'artifact' },
  'application/vnd.cncf.flux.config.v1+json': { label: 'Flux artifact', icon: 'artifact' },
  'application/vnd.cncf.notary.signature': { label: 'Notary signature', icon: 'signature' },
}

/** What cosign stores under its sha256-<hex>.<suffix> tags. */
const legacyTagKinds: [suffix: string, kind: Kind][] = [
  ['.sig', { label: 'Signature', icon: 'signature' }],
  ['.att', { label: 'Attestation', icon: 'attestation' }],
  ['.sbom', { label: 'SBOM', icon: 'sbom' }],
]

/** Signatures in formats Kokumi does not verify. */
const otherSignatureTypes = new Set(['application/vnd.cncf.notary.signature'])

const indexMediaTypes = new Set([
  'application/vnd.oci.image.index.v1+json',
  'application/vnd.docker.distribution.manifest.list.v2+json',
])

export function isSigstoreBundle(node: ArtifactNode): boolean {
  return node.artifactType === sigstoreBundleType
}

export function isIndex(node: ArtifactNode): boolean {
  return indexMediaTypes.has(node.mediaType)
}

/** Whether node is evidence: something attached to a manifest to vouch for it. */
export function isEvidence(node: ArtifactNode): boolean {
  return isSigstoreBundle(node) || node.link === 'Tag' || otherSignatureTypes.has(node.artifactType ?? '')
}

/** Whether node claims to be a signature, verified or not. */
function claimsSignature(node: ArtifactNode): boolean {
  if (isSigstoreBundle(node)) return node.predicateType === signaturePredicate
  if (node.link === 'Tag') return !!node.tag?.endsWith('.sig')
  return otherSignatureTypes.has(node.artifactType ?? '')
}

/**
 * What node is. A Sigstore bundle is only named after its statement once it
 * verified: anyone who can push to the repository can attach a bundle that
 * claims to be anything.
 */
export function kindOf(node: ArtifactNode): Kind {
  if (isIndex(node)) return { label: 'Image index', icon: 'index' }
  if (isSigstoreBundle(node)) {
    if (node.verification?.verified) return knownPredicates[node.predicateType ?? ''] ?? { label: 'Attestation', icon: 'attestation' }
    return { label: 'Sigstore bundle', icon: 'unknown' }
  }
  if (node.link === 'Tag' && node.tag) {
    const legacy = legacyTagKinds.find(([suffix]) => node.tag!.endsWith(suffix))
    if (legacy) return legacy[1]
  }
  return knownArtifactTypes[node.artifactType ?? ''] ?? { label: 'Artifact', icon: 'artifact' }
}

export function kindLabel(node: ArtifactNode): string {
  return kindOf(node).label
}

/**
 * The verifications of the signatures attached to node, one per signer. A
 * Sigstore bundle signs itself; its verification is on the node itself.
 */
export function signatures(node: ArtifactNode): Verification[] {
  const bySigner = new Map<string, Verification>()
  for (const r of node.referrers ?? []) {
    if (r.verification?.verified && claimsSignature(r)) {
      bySigner.set(r.verification.keyID ?? `${r.verification.issuer} ${r.verification.subject}`, r.verification)
    }
  }
  return [...bySigner.values()]
}

/**
 * Whether a trusted key or identity vouches for node, in a word or two. Who
 * it is, the inspector says.
 */
export function statusOf(node: ArtifactNode): Status {
  const v = node.verification
  if (v?.verified) return { tone: 'ok', label: 'Verified' }
  switch (v?.reason) {
    case 'UntrustedSigner':
      return { tone: 'neutral', label: 'Unknown signer' }
    case 'Unsupported':
      return { tone: 'neutral', label: 'Not checked' }
    case 'NotChecked':
      return { tone: 'neutral', label: 'Not checked yet' }
  }
  if (v) return { tone: 'bad', label: 'Not verified' }
  if (signatures(node).length > 0) return { tone: 'ok', label: 'Signed' }
  if (isEvidence(node)) return { tone: 'neutral', label: 'Not checked' }
  if ((node.referrers ?? []).some(claimsSignature)) return { tone: 'neutral', label: 'Signed · not checked' }
  return { tone: 'neutral', label: 'No signature' }
}

/** Where the tag the graph was looked up by points now, relative to its root. */
export interface TagState {
  tag: string
  /** The tag points at the root. */
  here: boolean
  /** Where the tag points instead; empty when it does not resolve. */
  moved?: string
}

export function tagState(graph: ArtifactGraph): TagState | undefined {
  if (!graph.tag || !graph.root) return undefined
  const here = graph.tagDigest === graph.root.digest
  return { tag: graph.tag, here, moved: here ? undefined : graph.tagDigest }
}

/** Explains the tag a graph was looked up by. */
export function tagExplanation(graph: ArtifactGraph): string | undefined {
  const tag = tagState(graph)
  if (!tag) return undefined
  if (tag.here) {
    return `The tag ${tag.tag} points at this digest. A tag is a name that can move: pushing another artifact under ${tag.tag} moves it there. The digest of this artifact never changes.`
  }
  if (tag.moved) {
    return `The tag ${tag.tag} has moved on to ${tag.moved}. This artifact is unchanged and stays reachable by its digest.`
  }
  return `The tag ${tag.tag} does not resolve any more. This artifact stays reachable by its digest.`
}

/** The org.opencontainers.image.created annotation, when present. */
export function createdAt(node: ArtifactNode): string | undefined {
  return node.annotations?.['org.opencontainers.image.created']
}

/** The nodes attached to node: index children first, then referrers. */
export function attachedTo(node: ArtifactNode): ArtifactNode[] {
  return [...(node.manifests ?? []), ...(node.referrers ?? [])]
}

/** Every node of the graph below and including node, depth first. */
export function flatten(node: ArtifactNode): ArtifactNode[] {
  return [node, ...attachedTo(node).flatMap(flatten)]
}
