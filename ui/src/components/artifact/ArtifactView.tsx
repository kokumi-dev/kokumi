import type { ArtifactGraph, ArtifactNode } from '../../api/types'
import GraphView, { type Note } from '../graph/GraphView'
import { useGraphSelection } from '../graph/useGraphSelection'
import { attachedTo } from './artifactKinds'
import ArtifactDetails from './ArtifactDetails'
import ArtifactTree from './ArtifactTree'
import type { BlobLinks, Reference } from './parts'

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

const readingHelp = (
  <>
    <li><strong>Every box is a manifest</strong>, named by its digest: the SHA-256 of its bytes. Same digest, same bytes.</li>
    <li><strong>Arrows point at what an artifact is about.</strong> A signature or SBOM names the manifest it belongs to in its <code>subject</code> (OCI 1.1).</li>
    <li><strong>Dashed</strong> lines attach by cosign's older tag name, <code>sha256-&lt;digest&gt;.sig</code>.</li>
    <li><strong>Dotted</strong> lines are digests written inside a manifest: index entries, <code>kokumi.dev/parent</code>, the base image.</li>
    <li><strong>A tag is a name that can move</strong>; the digest never changes.</li>
    <li><strong>✓</strong> means signed by a key the Kitchen trusts or, on a Product's source, as its <code>verify</code> requires. Content of anything unverified is never shown.</li>
  </>
)

/**
 * ArtifactView shows an artifact graph as a tree and, once a node is
 * selected, its details beside it.
 */
export default function ArtifactView({ graph, error, references = [], notes = [], blobLinks, help = true, keySource }: Props) {
  const { selected, toggle, clear } = useGraphSelection()
  const root = graph?.root
  const current = root && selected ? find(root, selected) : undefined

  return (
    <GraphView
      loading={!root}
      error={error ?? graph?.error}
      help={help && readingHelp}
      notes={notes}
      inspector={graph && current && (
        <ArtifactDetails
          key={current.node.digest}
          graph={graph}
          node={current.node}
          parent={current.parent}
          keySource={keySource}
          onClose={clear}
        />
      )}
    >
      {graph && root && (
        <ArtifactTree
          graph={{ ...graph, root }}
          selected={current?.node.digest}
          onSelect={(node) => toggle(node.digest)}
          references={references}
          blobLinks={blobLinks}
          keySource={keySource}
        />
      )}
    </GraphView>
  )
}
