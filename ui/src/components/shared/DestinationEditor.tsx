import { useEffect, useState } from 'react'
import type { OCIDestination, RegistryRef } from '../../api/types'
import { getDefaultRegistry } from '../../api/client'
import { SegmentedControl } from '../ui'
import { RegistryMatchHint } from './RegistryRefsEditor'
import styles from './Form.module.css'

// Destination mode: 'default' = in-cluster registry, 'url' = a URL of the user's.
type DestMode = 'default' | 'url'

interface DestinationEditorProps {
  destination: OCIDestination
  onChange: (dest: OCIDestination) => void
  namespace: string
  /** Resource name used in the OCI URL placeholder. */
  name?: string
  /** The Registries that authenticate the location, to say which one matches. */
  registryRefs: RegistryRef[]
  /** What the in-cluster default is, e.g. "Kokumi pushes to the in-cluster registry". */
  defaultHint: string
}

/**
 * DestinationEditor picks a location Kokumi pushes to: the in-cluster
 * default or a URL. It is used for the Order destination and the Product
 * vendor location, and says which of registryRefs authenticates the URL.
 */
export default function DestinationEditor({
  destination,
  onChange,
  namespace,
  name,
  registryRefs,
  defaultHint,
}: DestinationEditorProps) {
  const [mode, setMode] = useState<DestMode>(destination.url ? 'url' : 'default')
  const [defaultRegistry, setDefaultRegistry] = useState('')

  useEffect(() => {
    getDefaultRegistry()
      .then(({ baseURL }) => setDefaultRegistry(baseURL))
      .catch(() => {})
  }, [])

  function switchMode(next: DestMode) {
    setMode(next)
    onChange(next === 'default' ? {} : { url: destination.url ?? '' })
  }

  return (
    <div className={styles.fieldGroup}>
      <SegmentedControl
        aria-label="Destination"
        segments={[
          { id: 'default', label: 'In-cluster (default)' },
          { id: 'url', label: 'OCI URL' },
        ]}
        value={mode}
        onChange={switchMode}
      />
      {mode === 'url' && (
        <div className={styles.fieldGroup}>
          <label className={styles.label}>OCI URL</label>
          <input
            className={styles.input}
            value={destination.url ?? ''}
            onChange={(e) => onChange({ url: e.target.value })}
            placeholder={
              defaultRegistry
                ? `oci://${defaultRegistry}/${namespace || 'namespace'}/${name || 'name'}`
                : 'oci://ghcr.io/my-org/rendered-output'
            }
          />
          <RegistryMatchHint url={destination.url ?? ''} namespace={namespace} registryRefs={registryRefs} operation="push" />
        </div>
      )}
      {mode === 'default' && (
        <span className={styles.hint}>{defaultHint}, without credentials.</span>
      )}
    </div>
  )
}
