import { useEffect, useState } from 'react'
import { getProductSourceArtifact } from '../../api/client'
import type { ProductSourceArtifact } from '../../api/types'
import Section from '../../components/layout/Section'
import ArtifactView from '../../components/artifact/ArtifactView'
import type { Note } from '../../components/graph/GraphView'
import { flatten } from '../../components/artifact/artifactKinds'
import { plural, stripOCIScheme } from '../../utils/format'
import { useProductContext } from './productFormat'
import layout from '../../components/layout/layout.module.css'

/**
 * ProductArtifact draws the source a Product renders from as the registry
 * holds it, with the signatures, SBOMs and attestations its publisher
 * attached, and the vendored copy next to it.
 */
export default function ProductArtifact() {
  const { product } = useProductContext()
  const [artifact, setArtifact] = useState<ProductSourceArtifact | null>(null)
  const [error, setError] = useState<string | null>(null)
  const resolved = product.resolvedSource

  useEffect(() => {
    let cancelled = false
    getProductSourceArtifact(product.namespace, product.name)
      .then((a) => {
        if (cancelled) return
        setArtifact(a)
        setError(null)
      })
      .catch((e: Error) => {
        if (!cancelled) setError(e.message)
      })
    return () => {
      cancelled = true
    }
  }, [product.namespace, product.name, resolved?.url, resolved?.digest])

  const source = artifact?.source
  const vendored = artifact?.vendored
  const sourceVerified = product.conditions?.find((c) => c.type === 'SourceVerified')
  const sourceNotes: Note[] = []
  if (sourceVerified?.status === 'False') sourceNotes.push({ tone: 'warning', text: `The Product could not verify this source: ${sourceVerified.message}` })

  const sameDigest = !!source?.root && source.root.digest === vendored?.root?.digest
  const copied = new Set(vendored?.root ? flatten(vendored.root).map((n) => n.digest) : [])
  const notCopied = vendored?.root && source?.root ? flatten(source.root).filter((n) => !copied.has(n.digest)).length : 0
  const vendoredNotes: Note[] = []
  if (sameDigest) vendoredNotes.push({ tone: 'ok', text: 'Same digest as the source: the copy holds the same bytes.' })
  else if (source?.root) vendoredNotes.push({ tone: 'warning', text: 'The digest differs from the source.' })
  if (notCopied > 0) {
    vendoredNotes.push({
      tone: 'warning',
      text: `${plural(notCopied, 'artifact')} attached to the source ${notCopied === 1 ? 'is' : 'are'} not in the copy. Vendoring copies OCI referrers, but not cosign tags or anything attached after the copy was made.`,
    })
  }

  return (
    <>
      <Section
        title="Source"
        description={product.vendor ? 'The artifact at its source location, which the vendored copy was made from' : 'The artifact Orders render from'}
      >
        <ArtifactView graph={source ?? null} error={error} notes={sourceNotes} keySource={product.source.verify?.secretRef} />
      </Section>

      {vendored && (
        <Section title="Vendored copy" description={<>Orders render from this copy in <span className={layout.mono}>{stripOCIScheme(vendored.repository)}</span></>}>
          <ArtifactView graph={vendored} help={false} notes={vendoredNotes} keySource={product.source.verify?.secretRef} />
        </Section>
      )}
    </>
  )
}
