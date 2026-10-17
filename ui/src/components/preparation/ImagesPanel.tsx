import { useEffect, useState } from 'react'
import { getIntent } from '../../api/client'
import type { ArtifactConfig, ImageFieldSpec, Preparation } from '../../api/types'
import { imagePolicy } from '../../api/types'
import { Button, Counter, CopyIcon } from '../ui'
import Section from '../layout/Section'
import EmptyState from '../layout/EmptyState'
import PropertyList from '../layout/PropertyList'
import { describeImagePolicy, imageFieldSpecLabel } from '../../utils/products'
import { isMultiPlatform, pinnedReference } from '../../utils/images'
import styles from './ImagesPanel.module.css'

/** The image handling recorded in the intent; absent there means the default. */
function intentImages(config: ArtifactConfig | null): { policy?: string; fieldSpecs?: ImageFieldSpec[] } {
  const intent = config?.intent as { images?: { policy?: string; fieldSpecs?: ImageFieldSpec[] } } | undefined
  return intent?.images ?? {}
}

/**
 * ImagesPanel lists the container images a Preparation's manifests reference,
 * as they were resolved, pinned or vendored when it was rendered. The policy
 * and the reasons for unresolved images come from the artifact's config blob.
 */
export default function ImagesPanel({ preparation: prep }: { preparation: Preparation }) {
  const images = prep.images ?? []
  const [config, setConfig] = useState<ArtifactConfig | null>(null)
  const [copied, setCopied] = useState<string | null>(null)

  // The artifact is immutable, so it is read once per Preparation. Without
  // it the list still shows; only the policy and error details are missing.
  useEffect(() => {
    let cancelled = false
    getIntent(prep.namespace, prep.name)
      .then((c) => {
        if (!cancelled) setConfig(c)
      })
      .catch(() => {})
    return () => {
      cancelled = true
    }
  }, [prep.namespace, prep.name])

  const recorded = intentImages(config)
  const policy = imagePolicy(recorded.policy)
  const errors = new Map((config?.images ?? []).filter((i) => i.error).map((i) => [i.ref, i.error!]))

  function copy(reference: string) {
    void navigator.clipboard.writeText(reference)
    setCopied(reference)
  }

  return (
    <>
      <Section title="Image handling" description="Recorded in the artifact's intent when it was rendered">
        {config ? (
          <PropertyList
            items={[
              { label: 'Policy', value: <>{policy} <span className={styles.itemMeta}>· {describeImagePolicy(policy)}</span></> },
              {
                label: 'Image fields',
                value: (
                  <>
                    <span className={styles.itemMeta}>Containers, init and ephemeral containers, image volumes</span>
                    {(recorded.fieldSpecs ?? []).map((f) => (
                      <div key={imageFieldSpecLabel(f)} className={styles.mono}>{imageFieldSpecLabel(f)}</div>
                    ))}
                  </>
                ),
              },
            ]}
          />
        ) : (
          <p className={styles.note}>Loading…</p>
        )}
      </Section>

      <Section title={<>Images<Counter count={images.length} /></>} description="What the manifests run, by digest">
        {images.length === 0 ? (
          <EmptyState text="The manifests reference no container images." />
        ) : (
          <div className={styles.list}>
            {images.map((image) => {
              const reference = pinnedReference(image)
              return (
                <div key={image.ref} className={styles.item}>
                  <div className={styles.itemHeader}>
                    <span className={`${styles.itemTitle} ${styles.mono}`}>{image.ref}</span>
                    {image.mediaType && (
                      <span className={styles.itemMeta}>
                        {isMultiPlatform(image) ? 'Multi-platform' : 'Single platform'}
                      </span>
                    )}
                  </div>
                  {image.digest ? (
                    <span className={styles.mono}>{image.digest}</span>
                  ) : (
                    <span className={styles.unverified}>
                      Not resolved{errors.has(image.ref) ? `: ${errors.get(image.ref)}` : ''}
                    </span>
                  )}
                  {image.vendored && <span className={styles.itemMeta}>Pulled from <span className={styles.mono}>{image.vendored}</span></span>}
                  {reference && (
                    <div>
                      <Button variant="secondary" size="sm" icon={<CopyIcon />} onClick={() => copy(reference)}>
                        {copied === reference ? 'Copied' : 'Copy reference'}
                      </Button>
                    </div>
                  )}
                </div>
              )
            })}
          </div>
        )}
      </Section>
    </>
  )
}
