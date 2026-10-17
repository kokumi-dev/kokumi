import { useEffect, useState } from 'react'
import { Link } from 'react-router'
import { getIntent, getPreparationArtifact } from '../../api/client'
import type { ArtifactConfig, ArtifactGraph, Preparation } from '../../api/types'
import { imagePolicy } from '../../api/types'
import Section from '../../components/layout/Section'
import ArtifactView from '../../components/artifact/ArtifactView'
import type { Reference } from '../../components/artifact/parts'
import { paths } from '../../routes/paths'
import { shortDigest } from '../../utils/format'
import { imageFullName, imageShortName, pinnedReference } from '../../utils/images'
import { usePreparationContext } from './preparationContext'
import layout from '../../components/layout/layout.module.css'

const annotationParent = 'kokumi.dev/parent'
const annotationBaseName = 'org.opencontainers.image.base.name'
const annotationBaseDigest = 'org.opencontainers.image.base.digest'

/** How many images the card names before it points at the Images tab. */
const maxCardImages = 6

/**
 * PreparationArtifact draws the Preparation's artifact as the registry holds
 * it: the manifest, its config (the intent) and layer (the rendered YAML),
 * the images its config records, and the signature, provenance and approvals
 * attached to it.
 */
export default function PreparationArtifact() {
  const { prep, parent, order } = usePreparationContext()
  const [graph, setGraph] = useState<ArtifactGraph | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [config, setConfig] = useState<ArtifactConfig | null>(null)

  // Reload when the Preparation's state changes (delivered via SSE): that is
  // when signatures, provenance and sealed approvals are attached.
  useEffect(() => {
    let cancelled = false
    getPreparationArtifact(prep.namespace, prep.name)
      .then((g) => {
        if (cancelled) return
        setGraph(g)
        setError(null)
      })
      .catch((e: Error) => {
        if (!cancelled) setError(e.message)
      })
    return () => {
      cancelled = true
    }
  }, [prep.namespace, prep.name, prep.state, prep.approval?.sealedAt])

  // The images come from the config blob, which the signature covers. A
  // caller who may not read the Order's intent sees no image rows.
  useEffect(() => {
    let cancelled = false
    getIntent(prep.namespace, prep.name)
      .then((c) => !cancelled && setConfig(c))
      .catch(() => !cancelled && setConfig(null))
    return () => {
      cancelled = true
    }
  }, [prep.namespace, prep.name])

  const annotations = graph?.root?.annotations ?? {}
  const references: Reference[] = []
  const parentDigest = annotations[annotationParent]
  if (parentDigest) {
    references.push({
      label: 'parent',
      value: parent ? (
        <Link className={layout.inlineLink} to={paths.preparation(parent.namespace, parent.name, 'artifact')}>
          {parent.name}
        </Link>
      ) : (
        <span title={parentDigest}>{shortDigest(parentDigest)}… (not in this cluster)</span>
      ),
    })
  }
  const baseDigest = annotations[annotationBaseDigest]
  if (baseDigest) {
    const baseName = annotations[annotationBaseName] ?? 'source'
    const base = `${baseName.split('/').pop()}@${shortDigest(baseDigest)}…`
    const title = `${baseName}@${baseDigest}`
    references.push({
      label: 'base',
      value: order ? (
        <Link className={layout.inlineLink} to={paths.product(prep.namespace, order.productRef.name, 'artifact')} title={title}>
          {base}
        </Link>
      ) : (
        <span title={title}>{base}</span>
      ),
    })
  }

  references.push(...imageReferences(prep, config))

  return (
    <Section
      title="Artifact"
      description="The OCI artifact in the registry and everything attached to it, read live"
    >
      <ArtifactView
        key={prep.name}
        graph={graph}
        error={error}
        references={references}
        blobLinks={{
          config: <Link className={layout.inlineLink} to={paths.preparation(prep.namespace, prep.name, 'intent')}>Intent</Link>,
          layers: <Link className={layout.inlineLink} to={paths.preparation(prep.namespace, prep.name, 'files')}>Files</Link>,
        }}
      />
    </Section>
  )
}

/**
 * The rows of the card that name the images the config records with a
 * digest, up to maxCardImages, each linking to the Images tab.
 */
function imageReferences(prep: Preparation, config: ArtifactConfig | null): Reference[] {
  const images = (config?.images ?? []).filter((image) => image.digest)
  const policy = imagePolicy((config?.intent as { images?: { policy?: string } } | undefined)?.images?.policy)
  const pinned = policy !== 'Record'
  const imagesTab = paths.preparation(prep.namespace, prep.name, 'images')
  const rows: Reference[] = images.slice(0, maxCardImages).map((image) => ({
    key: `image:${image.ref}`,
    label: 'image',
    value: (
      <Link
        className={layout.inlineLink}
        to={imagesTab}
        title={pinned
          ? `${imageFullName(image)}, pulled by digest from ${pinnedReference(image)}`
          : `${imageFullName(image)}, resolved to this digest when rendered; the manifests pull the tag`}
      >
        {imageShortName(image)}@{shortDigest(image.digest!)}…
      </Link>
    ),
  }))
  if (images.length > maxCardImages) {
    rows.push({
      key: 'image:more',
      label: '',
      value: <Link className={layout.inlineLink} to={imagesTab}>and {images.length - maxCardImages} more</Link>,
    })
  }
  return rows
}
