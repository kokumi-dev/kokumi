import { useEffect, useState } from 'react'
import { Link } from 'react-router'
import { getIntent } from '../../api/client'
import type { ArtifactConfig, Preparation } from '../../api/types'
import Section from '../layout/Section'
import PropertyList from '../layout/PropertyList'
import { formatDate } from '../../utils/format'
import { paths } from '../../routes/paths'
import layout from '../layout/layout.module.css'
import styles from './IntentPanel.module.css'

/**
 * IntentPanel shows the config blob stored inside a Preparation's artifact:
 * what was asked for, what it was built from, and what rendered it.
 */
export default function IntentPanel({ preparation: prep }: { preparation: Preparation }) {
  const [config, setConfig] = useState<ArtifactConfig | null>(null)
  const [error, setError] = useState<string | null>(null)

  // The artifact is immutable, so it is read once per Preparation.
  useEffect(() => {
    let cancelled = false
    getIntent(prep.namespace, prep.name)
      .then((c) => {
        if (!cancelled) setConfig(c)
      })
      .catch((e: Error) => {
        if (!cancelled) setError(e.message)
      })
    return () => {
      cancelled = true
    }
  }, [prep.namespace, prep.name])

  if (error) {
    return (
      <Section title="Intent">
        <p className={styles.error}>{error}</p>
      </Section>
    )
  }

  if (!config) {
    return (
      <Section title="Intent">
        <p className={styles.note}>Loading…</p>
      </Section>
    )
  }

  return (
    <>
      <Section title="Built from" description="Recorded in the artifact itself and readable by anyone who can pull it">
        <PropertyList
          items={[
            { label: 'Source', value: <span className={layout.mono}>{config.source.oci}</span> },
            { label: 'Version', value: config.source.version },
            { label: 'Source digest', value: <span className={layout.mono}>{config.source.digest}</span> },
            { label: 'Destination', value: <span className={layout.mono}>{config.destination.oci}</span> },
            {
              label: 'Product',
              value: config.product && (
                <Link className={layout.inlineLink} to={paths.product(config.product.namespace, config.product.name)}>
                  {config.product.name}
                </Link>
              ),
              show: !!config.product,
            },
            { label: 'Parent', value: <span className={layout.mono}>{config.parent}</span>, show: !!config.parent },
            { label: 'Rendered', value: formatDate(config.createdTime) },
            { label: 'Config hash', value: <span className={layout.mono}>{config.configHash}</span>, show: !!config.configHash },
          ]}
        />
      </Section>

      <Section title="Intent" description="The merged configuration the config hash is computed from">
        <pre className={styles.code}>{JSON.stringify(config.intent ?? {}, null, 2)}</pre>
      </Section>
    </>
  )
}
