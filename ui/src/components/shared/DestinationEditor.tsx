import { useEffect, useState } from 'react'
import type { OCIDestination } from '../../api/types'
import { getDefaultRegistry } from '../../api/client'
import { usePantries } from '../../hooks/usePantries'
import styles from '../order/OrderFormModal.module.css'

// Destination mode: 'default' = in-cluster registry, 'oci' = direct URL, 'pantry' = Pantry provides URL.
type DestMode = 'default' | 'oci' | 'pantry'

interface DestinationEditorProps {
  destination: OCIDestination
  onChange: (dest: OCIDestination) => void
  /** Namespace used to filter the pantry dropdown. */
  namespace: string
  /** Resource name used in the OCI URL placeholder. */
  name?: string
  /** Path segment shown in the OCI URL placeholder (e.g. "rendered"). */
  pathHint?: string
}

/**
 * DestinationEditor renders the shared three-tab destination picker
 * (In-cluster default / Direct OCI URL / From Pantry) used by both the
 * Order and Menu forms.
 */
export default function DestinationEditor({
  destination,
  onChange,
  namespace,
  name,
  pathHint,
}: DestinationEditorProps) {
  const pantries = usePantries()

  const [mode, setMode] = useState<DestMode>(
    destination.pantryRef?.name ? 'pantry'
      : destination.oci ? 'oci'
      : 'default',
  )
  const [defaultRegistry, setDefaultRegistry] = useState('')

  useEffect(() => {
    getDefaultRegistry()
      .then(({ baseURL }) => setDefaultRegistry(baseURL))
      .catch(() => {})
  }, [])

  function switchMode(next: DestMode) {
    setMode(next)
    if (next === 'default') {
      // In-cluster default — clear explicit destination.
      onChange({})
    } else if (next === 'oci') {
      onChange({ oci: destination.oci ?? '' })
    } else {
      onChange({ pantryRef: { name: destination.pantryRef?.name ?? '' } })
    }
  }

  return (
    <div className={styles.fieldGroup}>
      <div className={styles.tabs} style={{ marginBottom: 0 }}>
        <button
          type="button"
          className={`${styles.tab} ${mode === 'default' ? styles.tabActive : ''}`}
          onClick={() => switchMode('default')}
        >
          In-cluster (default)
        </button>
        <button
          type="button"
          className={`${styles.tab} ${mode === 'oci' ? styles.tabActive : ''}`}
          onClick={() => switchMode('oci')}
        >
          Direct OCI URL
        </button>
        <button
          type="button"
          className={`${styles.tab} ${mode === 'pantry' ? styles.tabActive : ''}`}
          onClick={() => switchMode('pantry')}
        >
          From Pantry
        </button>
      </div>
      {mode === 'oci' && (
        <div className={styles.fieldGroup}>
          <label className={styles.label}>Destination OCI URL</label>
          <input
            className={styles.input}
            value={destination.oci ?? ''}
            onChange={(e) => onChange({ oci: e.target.value })}
            placeholder={
              defaultRegistry
                ? `oci://${defaultRegistry}${pathHint ? `/${pathHint}` : ''}/${namespace || 'namespace'}/${name || 'name'}`
                : 'oci://ghcr.io/my-org/rendered-output'
            }
          />
        </div>
      )}
      {mode === 'pantry' && (
        <div className={styles.fieldGroup}>
          <label className={styles.label}>Pantry</label>
          <select
            className={styles.input}
            value={destination.pantryRef?.name ?? ''}
            onChange={(e) => onChange({ pantryRef: { name: e.target.value } })}
          >
            <option value="">— select a pantry —</option>
            {(pantries ?? []).filter((p) => p.namespace === namespace).map((p) => (
              <option key={p.name} value={p.name}>{p.name}</option>
            ))}
          </select>
        </div>
      )}
      {mode === 'default' && defaultRegistry && (
        <span style={{ fontSize: '0.75rem', color: 'var(--color-text-muted-light)' }}>
          Kokumi will push to the in-cluster registry automatically
        </span>
      )}
    </div>
  )
}
