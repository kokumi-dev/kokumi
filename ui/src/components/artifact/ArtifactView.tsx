import { useEffect, useState } from 'react'
import type { ArtifactGraph, ArtifactNode } from '../../api/types'
import { Button } from '../ui'
import { attachedTo } from './artifactKinds'
import ArtifactDetails from './ArtifactDetails'
import ArtifactTree from './ArtifactTree'
import type { BlobLinks, Note, Reference } from './parts'
import styles from './ArtifactView.module.css'

interface Props {
  /** null while loading. */
  graph: ArtifactGraph | null
  /** Why the graph could not be loaded. */
  error?: string | null
  /** Digests the root names in its annotations, such as kokumi.dev/parent. */
  references?: Reference[]
  /** Lines of explanation shown below the graph. */
  notes?: Note[]
  blobLinks?: BlobLinks
  /** Offer the reading help; off for a second graph on a page. */
  help?: boolean
  /** Where keys the Kitchen does not trust come from, such as the Secret of a Product's verify. */
  keySource?: string
}

/** The node with digest in the graph below node, and the node it is attached to. */
function find(node: ArtifactNode, digest: string, parent?: ArtifactNode): { node: ArtifactNode; parent?: ArtifactNode } | undefined {
  if (node.digest === digest) return { node, parent }
  for (const child of attachedTo(node)) {
    const found = find(child, digest, node)
    if (found) return found
  }
  return undefined
}

/**
 * ArtifactView shows an artifact graph as a tree and, once a node is
 * selected, its details beside it. A click anywhere outside the details and
 * the nodes, or Escape, closes them.
 */
export default function ArtifactView({ graph, error, references = [], notes = [], blobLinks, help = true, keySource }: Props) {
  const [selected, setSelected] = useState<string | null>(null)
  const [helpOpen, setHelpOpen] = useState(false)

  useEffect(() => {
    if (!selected) return
    const onPointerDown = (e: PointerEvent) => {
      const target = e.target as Element | null
      if (target?.closest('[data-artifact-details], [data-artifact-node]')) return
      setSelected(null)
    }
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setSelected(null)
    }
    document.addEventListener('pointerdown', onPointerDown)
    document.addEventListener('keydown', onKeyDown)
    return () => {
      document.removeEventListener('pointerdown', onPointerDown)
      document.removeEventListener('keydown', onKeyDown)
    }
  }, [selected])

  if (error || graph?.error) return <p className={styles.error}>{error ?? graph?.error}</p>
  if (!graph?.root) return <p className={styles.loading}>Loading…</p>

  const root = graph.root
  const current = selected ? find(root, selected) : undefined

  return (
    <div className={styles.view}>
      {help && (
        <div className={styles.toolbar}>
          <Button variant="ghost" size="sm" onClick={() => setHelpOpen(!helpOpen)} aria-expanded={helpOpen}>
            {helpOpen ? 'Hide help' : 'How to read this'}
          </Button>
        </div>
      )}

      {helpOpen && (
        <ul className={styles.help}>
          <li><strong>Every box is a manifest</strong>, named by its digest: the SHA-256 of its bytes. Same digest, same bytes.</li>
          <li><strong>Arrows point at what an artifact is about.</strong> A signature or SBOM names the manifest it belongs to in its <code>subject</code> (OCI 1.1).</li>
          <li><strong>Dashed</strong> lines attach by cosign's older tag name, <code>sha256-&lt;digest&gt;.sig</code>.</li>
          <li><strong>Dotted</strong> lines are digests written inside a manifest: index entries, <code>kokumi.dev/parent</code>, the base image.</li>
          <li><strong>A tag is a name that can move</strong>; the digest never changes.</li>
          <li><strong>✓</strong> means signed by a key the Kitchen trusts or, on a Product's source, as its <code>verify</code> requires. Content of anything unverified is never shown.</li>
        </ul>
      )}

      <div className={`${styles.body} ${current ? styles.bodyInspecting : ''}`}>
        <div className={styles.stage}>
          <div className={styles.stageInner}>
            <ArtifactTree
              graph={{ ...graph, root }}
              selected={current?.node.digest}
              onSelect={(node) => setSelected(node.digest === selected ? null : node.digest)}
              references={references}
              blobLinks={blobLinks}
              keySource={keySource}
            />
          </div>
        </div>
        {current && (
          <ArtifactDetails
            key={current.node.digest}
            graph={graph}
            node={current.node}
            parent={current.parent}
            keySource={keySource}
            onClose={() => setSelected(null)}
          />
        )}
      </div>

      {notes.length > 0 && (
        <ul className={styles.notes}>
          {notes.map((note, i) => (
            <li key={i} className={note.tone === 'ok' ? styles.noteOk : note.tone === 'warning' ? styles.noteWarning : undefined}>
              {note.text}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
