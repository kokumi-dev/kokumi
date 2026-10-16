import type { ArtifactGraph, ArtifactNode } from '../../api/types'
import { formatBytes, shortDigest } from '../../utils/format'
import { attachedTo } from './artifactKinds'
import { NodeButton, NodeSummary, SignerMark, TagPill, type BlobLinks, type Reference } from './parts'
import { signersOf } from './signer'
import styles from './ArtifactTree.module.css'

interface Props {
  graph: ArtifactGraph & { root: ArtifactNode }
  selected?: string
  onSelect: (node: ArtifactNode) => void
  /** Digests the root names in its annotations, such as kokumi.dev/parent. */
  references: Reference[]
  blobLinks?: BlobLinks
  /** Where keys the Kitchen does not trust come from, such as the Secret of a Product's verify. */
  keySource?: string
}

const linkClass: Record<string, string> = {
  Subject: styles.linkSubject,
  Tag: styles.linkTag,
  Index: styles.linkIndex,
}

/**
 * ArtifactTree draws each manifest above the artifacts attached to it, with
 * an arrow up to the manifest they are about.
 */
export default function ArtifactTree({ graph, selected, onSelect, references, blobLinks, keySource }: Props) {
  const root = graph.root
  const rootSigners = signersOf(root, keySource)
  const blobs = [...(root.config ? [{ role: 'config', blob: root.config, link: blobLinks?.config }] : []),
    ...(root.layers ?? []).map((blob) => ({ role: 'layer', blob, link: blobLinks?.layers }))]

  return (
    <div className={styles.tree}>
      <div className={`${styles.branch} ${styles.branchRoot}`}>
        <div className={`${styles.rootCard} ${selected === root.digest ? styles.selected : ''}`}>
          <div className={styles.rootHeader}>
            <NodeButton digest={root.digest} selected={selected === root.digest} className={styles.rootButton} onClick={() => onSelect(root)}>
              <NodeSummary node={root} />
            </NodeButton>
            <TagPill graph={graph} />
          </div>
          <div className={styles.rootMeta}>
            <span className={styles.mono} title={root.digest}>{shortDigest(root.digest)}</span>
            <span>{formatBytes(root.size)}</span>
            {rootSigners.map((s) => <SignerMark key={s.full} signer={s} variant="chip" />)}
          </div>
          {blobs.length > 0 && (
            <ul className={styles.rows} aria-label="Contents">
              {blobs.map(({ role, blob, link }) => (
                <li key={`${role}:${blob.digest}`} className={styles.row}>
                  <span className={styles.rowLabel}>{role}</span>
                  <span className={styles.rowValue}>{formatBytes(blob.size)}</span>
                  {link && <span className={styles.rowLink}>{link}</span>}
                </li>
              ))}
            </ul>
          )}
          {references.length > 0 && (
            <ul className={styles.rows} aria-label="References">
              {references.map((ref) => (
                <li key={ref.label} className={styles.row}>
                  <span className={styles.rowLabel}>{ref.label}</span>
                  <span className={styles.refArrow} aria-hidden />
                  <span className={styles.rowRef}>{ref.value}</span>
                </li>
              ))}
            </ul>
          )}
        </div>
        <Attached node={root} selected={selected} onSelect={onSelect} />
      </div>
    </div>
  )
}

function Attached({ node, selected, onSelect }: { node: ArtifactNode; selected?: string; onSelect: (node: ArtifactNode) => void }) {
  const attached = attachedTo(node)
  if (attached.length === 0 && !node.truncated) return null

  return (
    <ul className={`${styles.children} ${node.referrers?.length ? styles.pointsUp : ''}`}>
      {attached.map((child) => (
        <li key={child.digest} className={`${styles.item} ${child.link ? linkClass[child.link] : ''}`}>
          <span className={styles.elbow} aria-hidden />
          <div className={styles.branch}>
            <NodeButton
              digest={child.digest}
              selected={selected === child.digest}
              className={`${styles.card} ${selected === child.digest ? styles.selected : ''}`}
              onClick={() => onSelect(child)}
            >
              <NodeSummary node={child} />
            </NodeButton>
            <Attached node={child} selected={selected} onSelect={onSelect} />
          </div>
        </li>
      ))}
      {node.truncated && (
        <li className={`${styles.item} ${styles.linkSubject}`}>
          <span className={styles.elbow} aria-hidden />
          <span className={styles.more}>More is attached than shown</span>
        </li>
      )}
    </ul>
  )
}
