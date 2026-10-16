import { useState, type ReactNode } from 'react'
import type { ArtifactGraph, ArtifactNode, Descriptor } from '../../api/types'
import { CloseIcon, CopyIcon, Disclosure, IconButton } from '../ui'
import { formatBytes, formatDate, shortDigest } from '../../utils/format'
import {
  createdAt, isEvidence, isIndex, kindLabel, kindOf, signatures, statusOf, tagExplanation,
} from './artifactKinds'
import { ArtifactIcon, SignerMark, StatusMark } from './parts'
import { signersOf } from './signer'
import styles from './ArtifactView.module.css'

interface Props {
  graph: ArtifactGraph
  node: ArtifactNode
  /** The manifest node is attached to; absent for the root. */
  parent?: ArtifactNode
  /** Where keys the Kitchen does not trust come from, such as the Secret of a Product's verify. */
  keySource?: string
  onClose: () => void
}

/** How node is attached to its parent, in words. */
function linkText(node: ArtifactNode, parent?: ArtifactNode): string {
  const target = parent ? `the ${kindLabel(parent)} (${shortDigest(parent.digest)}…)` : 'its parent'
  switch (node.link) {
    case 'Subject':
      return `Attached to ${target}: its subject field names that digest, the OCI 1.1 way of attaching artifacts.`
    case 'Tag':
      return `Attached to ${target} by the tag ${node.tag}, the naming convention cosign used before OCI 1.1.`
    case 'Index':
      return `Listed by digest in ${target}.`
    default:
      return 'The artifact this view starts from.'
  }
}

/** Why nobody vouches for node, in a sentence; nothing when someone does. */
function trustText(node: ArtifactNode): string | undefined {
  const v = node.verification
  if (v?.verified) return undefined
  const detail = v?.error ? `: ${v.error}` : ''
  switch (v?.reason) {
    case 'UntrustedSigner':
      return `Signed by a key or identity that is not trusted${detail}. The signature may well be valid; its content is not shown.`
    case 'Unsupported':
      return `Not checked${detail || ': Kokumi does not verify this signature format'}.`
    case 'NotChecked':
      return 'Not checked yet: it was attached after the Product last checked its signatures. The next reconcile checks it.'
  }
  if (v) return `Did not verify${detail}. Its content is not shown.`

  if (signatures(node).length > 0) return undefined
  if (isEvidence(node)) return 'Not checked: here Kokumi verifies Sigstore bundles against the keys the Kitchen trusts only.'
  return 'No signature Kokumi could verify is attached to it.'
}

function Fact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className={styles.fact}>
      <dt className={styles.factLabel}>{label}</dt>
      <dd className={styles.factValue}>{children}</dd>
    </div>
  )
}

/** ArtifactDetails is the inspector of one node: what it is, how it is attached, and who vouches for it. */
export default function ArtifactDetails({ graph, node, parent, keySource, onClose }: Props) {
  const [open, setOpen] = useState<string | null>(null)
  const kind = kindOf(node)
  const status = statusOf(node)
  const created = createdAt(node)
  const blobs: [string, Descriptor][] = [
    ...(node.config ? [['config', node.config] as [string, Descriptor]] : []),
    ...(node.layers ?? []).map((l) => ['layer', l] as [string, Descriptor]),
  ]
  const annotations = Object.entries(node.annotations ?? {})
  const tagText = node.digest === graph.root?.digest ? tagExplanation(graph) : undefined
  const toggle = (id: string) => (next: boolean) => setOpen(next ? id : null)
  const signers = signersOf(node, keySource)
  const trust = trustText(node)

  return (
    <aside className={styles.inspector} aria-label={`${kind.label} details`} data-artifact-details>
      <div className={styles.inspectorHead}>
        <ArtifactIcon kind={kind.icon} size="lg" muted={status.tone !== 'ok'} />
        <div className={styles.inspectorTitle}>
          <span>{kind.label}</span>
          <StatusMark status={status} />
        </div>
        <IconButton aria-label="Close details" onClick={onClose}><CloseIcon /></IconButton>
      </div>

      <div className={styles.prose}>
        <p>{linkText(node, parent)}</p>
        {trust && <p>{trust}</p>}
        {tagText && <p>{tagText}</p>}
      </div>

      {signers.length > 0 && (
        <div className={styles.signers}>
          {signers.map((s) => (
            <div key={s.full} className={styles.signerBlock}>
              <span className={styles.factLabel}>{s.keyless ? 'Signed keyless by' : 'Signed with key'}</span>
              <SignerMark signer={s} detail />
              <span className={styles.signerFull}>{s.full}</span>
            </div>
          ))}
        </div>
      )}

      <dl className={styles.facts}>
        <Fact label="Digest">
          <span className={styles.digestRow}>
            <span className={styles.monoWrap}>{node.digest}</span>
            <IconButton size="xs" aria-label="Copy digest" onClick={() => void navigator.clipboard?.writeText(node.digest).catch(() => {})}>
              <CopyIcon />
            </IconButton>
          </span>
        </Fact>
        {node.artifactType && <Fact label="Type"><span className={styles.monoWrap}>{node.artifactType}</span></Fact>}
        {node.predicateType && <Fact label="Statement"><span className={styles.monoWrap}>{node.predicateType}</span></Fact>}
        <Fact label="Size">{formatBytes(node.size)} manifest{blobs.length > 0 ? `, ${formatBytes(blobs.reduce((s, [, b]) => s + b.size, 0))} content` : ''}</Fact>
        {created && <Fact label="Created">{formatDate(created)}</Fact>}
        {isIndex(node) && <Fact label="Contains">{node.manifests?.length ?? 0} manifests</Fact>}
      </dl>

      <div className={styles.disclosures}>
        {blobs.length > 0 && (
          <Disclosure tone="inline" label={`Blobs · ${blobs.length}`} open={open === 'blobs'} onOpenChange={toggle('blobs')}>
            <dl className={styles.facts}>
              {blobs.map(([role, blob]) => (
                <Fact key={`${role}:${blob.digest}`} label={`${role} · ${formatBytes(blob.size)}`}>
                  <span className={styles.monoWrap}>{blob.mediaType}</span>
                  <span className={styles.monoWrap}>{blob.digest}</span>
                </Fact>
              ))}
            </dl>
          </Disclosure>
        )}
        {annotations.length > 0 && (
          <Disclosure tone="inline" label={`Annotations · ${annotations.length}`} open={open === 'annotations'} onOpenChange={toggle('annotations')}>
            <dl className={styles.facts}>
              {annotations.map(([key, value]) => (
                <Fact key={key} label={key}>
                  <span className={styles.monoWrap}>{value}</span>
                </Fact>
              ))}
            </dl>
          </Disclosure>
        )}
        {node.verification?.verified && node.payload !== undefined && (
          <Disclosure tone="inline" label="Signed statement" open={open === 'statement'} onOpenChange={toggle('statement')}>
            <pre className={styles.code}>{JSON.stringify(node.payload, null, 2)}</pre>
          </Disclosure>
        )}
      </div>

      {node.error && <p className={styles.error}>Not everything attached could be read: {node.error}</p>}
    </aside>
  )
}
