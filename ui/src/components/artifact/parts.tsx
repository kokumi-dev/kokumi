import type { ReactNode } from 'react'
import type { ArtifactGraph, ArtifactNode } from '../../api/types'
import { shortDigest } from '../../utils/format'
import { Chip, NodeIcon, NodeSummary, TagIcon, type IconTone } from '../graph/parts'
import { kindOf, statusOf, tagExplanation, tagState, type IconKind } from './artifactKinds'
import type { Signer, SignerIcon as SignerIconKind } from './signer'
import styles from './parts.module.css'

/** Something a manifest names by digest outside OCI's own fields, such as kokumi.dev/parent. */
export interface Reference {
  label: string
  value: ReactNode
  /** Tells apart references with the same label; defaults to the label. */
  key?: string
}

/** What the root's config and layers link to, e.g. the tab that shows them. */
export interface BlobLinks {
  config?: ReactNode
  layers?: ReactNode
}

const iconPaths: Record<IconKind, ReactNode> = {
  artifact: (
    <>
      <path d="M8 1.75 13.5 4.6v6.8L8 14.25 2.5 11.4V4.6L8 1.75Z" />
      <path d="M2.5 4.6 8 7.5l5.5-2.9M8 7.5v6.75" />
    </>
  ),
  signature: (
    <>
      <path d="M8 1.6 9.5 2.8l1.9-.2.6 1.8 1.6 1.1-.6 1.8.6 1.8-1.6 1.1-.6 1.8-1.9-.2L8 13.4l-1.5-1.2-1.9.2-.6-1.8-1.6-1.1.6-1.8-.6-1.8 1.6-1.1.6-1.8 1.9.2L8 1.6Z" />
      <path d="m5.9 7.6 1.4 1.4 2.8-2.9" />
    </>
  ),
  attestation: (
    <>
      <path d="M4 1.75h5.25L12.25 4.75v9.5H4V1.75Z" />
      <path d="M9.25 1.75v3h3M6.25 8h3.75M6.25 10.75h3.75" />
    </>
  ),
  approvals: (
    <>
      <circle cx="6" cy="5" r="2.5" />
      <path d="M1.75 13.75c.6-2.3 2.2-3.5 4.25-3.5 1.1 0 2.1.35 2.85 1.05" />
      <path d="m9.75 11.5 1.5 1.5 3-3.25" />
    </>
  ),
  sbom: (
    <>
      <path d="M6 4h7.5M6 8h7.5M6 12h7.5" />
      <path d="M2.75 4h.5M2.75 8h.5M2.75 12h.5" />
    </>
  ),
  index: (
    <>
      <path d="M8 2 14 5 8 8 2 5 8 2Z" />
      <path d="m2 8 6 3 6-3M2 11l6 3 6-3" />
    </>
  ),
  unknown: (
    <>
      <circle cx="8" cy="8" r="6.25" />
      <path d="M6.4 6.2a1.7 1.7 0 1 1 2.35 1.6c-.5.2-.75.6-.75 1.1v.35M8 11.25v.1" />
    </>
  ),
}

const iconTones: Record<IconKind, IconTone> = {
  artifact: 'accent',
  signature: 'success',
  approvals: 'success',
  attestation: 'warning',
  sbom: 'warning',
  index: 'muted',
  unknown: 'muted',
}

/**
 * The icon of a kind of node, in a soft tile. Evidence only takes its colour
 * once it verified, so colour never suggests trust that is not there.
 */
export function ArtifactIcon({ kind, size = 'md', muted }: { kind: IconKind; size?: 'md' | 'lg'; muted?: boolean }) {
  return (
    <NodeIcon tone={muted && kind !== 'artifact' ? 'muted' : iconTones[kind]} size={size}>
      {iconPaths[kind]}
    </NodeIcon>
  )
}

/** The kind and status of a node, the status below the kind. */
export function ArtifactSummary({ node }: { node: ArtifactNode }) {
  const kind = kindOf(node)
  const status = statusOf(node)
  return <NodeSummary icon={<ArtifactIcon kind={kind.icon} muted={status.tone !== 'ok'} />} title={kind.label} status={status} />
}

const signerIconPaths: Record<SignerIconKind, ReactNode> = {
  key: (
    <>
      <circle cx="5.5" cy="10.5" r="3" />
      <path d="m7.6 8.4 6-6M11.6 4.4l1.6 1.6M9.9 6.1l1.3 1.3" />
    </>
  ),
  github: (
    <path
      fill="currentColor"
      stroke="none"
      d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z"
    />
  ),
  gitlab: <path d="M8 14.25 1.75 9.6l1.1-3.6L4.25 1.75l1.5 4.25h4.5l1.5-4.25L13.15 6l1.1 3.6L8 14.25Z" />,
  person: (
    <>
      <circle cx="8" cy="5.5" r="2.75" />
      <path d="M2.75 14c.6-2.6 2.6-4 5.25-4s4.65 1.4 5.25 4" />
    </>
  ),
  identity: (
    <>
      <rect x="1.75" y="3" width="12.5" height="10" rx="2" />
      <circle cx="5.75" cy="7.25" r="1.5" />
      <path d="M9.25 6.5h3M9.25 9h3M3.75 11c.4-1 1.1-1.5 2-1.5s1.6.5 2 1.5" />
    </>
  ),
}

/** The icon of a signer: a key, or the platform of a keyless identity. */
export function SignerIcon({ icon }: { icon: SignerIconKind }) {
  return (
    <svg className={styles.signerIcon} viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      {signerIconPaths[icon]}
    </svg>
  )
}

/**
 * SignerMark shows who signed: an icon and a short name, optionally with
 * what tells them apart. Its title holds the full identity.
 */
export function SignerMark({ signer, detail, variant = 'plain' }: { signer: Signer; detail?: boolean; variant?: 'plain' | 'chip' }) {
  const title = `${signer.keyless ? 'Keyless' : 'Key'}: ${signer.full}`
  const content = (
    <>
      <span className={styles.signerTitle}>{signer.title}</span>
      {detail && signer.detail && <span className={styles.signerDetail}>{signer.detail}</span>}
    </>
  )
  if (variant === 'chip') return <Chip icon={<SignerIcon icon={signer.icon} />} title={title}>{content}</Chip>
  return (
    <span className={styles.signer} title={title}>
      <SignerIcon icon={signer.icon} />
      {content}
    </span>
  )
}

/** The tag a graph was looked up by, and whether it still points at the root. */
export function TagPill({ graph }: { graph: ArtifactGraph }) {
  const tag = tagState(graph)
  if (!tag) return null
  return (
    <Chip icon={<TagIcon />} tone={tag.here ? 'plain' : 'faded'} mono title={tagExplanation(graph)} className={styles.tag}>
      {tag.tag}
      {!tag.here && <span className={styles.tagNote}>{tag.moved ? `moved to ${shortDigest(tag.moved)}…` : 'gone'}</span>}
    </Chip>
  )
}
