import type { Registry, RegistryRef } from '../../api/types'
import { useRegistries } from '../../hooks/useRegistries'
import { matchRegistry, referencedRegistries } from '../../utils/registries'
import { stripOCIScheme } from '../../utils/format'
import { Button } from '../ui'
import styles from './Form.module.css'

interface RegistryRefsEditorProps {
  namespace: string
  value: RegistryRef[]
  onChange: (refs: RegistryRef[]) => void
  /** What the listed Registries authenticate, e.g. "the source, the images and the vendor location". */
  owns: string
  /** Leaves out the label, for a section that is titled already. */
  unlabeled?: boolean
  /** Shows the selection without letting it change. */
  disabled?: boolean
}

/**
 * RegistryRefsEditor picks the Registries of a namespace an object connects
 * through. Each location the object owns uses the listed Registry with the
 * longest matching url, and no Registry when none matches.
 */
export default function RegistryRefsEditor({ namespace, value, onChange, owns, unlabeled, disabled }: RegistryRefsEditorProps) {
  const registries = useRegistries()
  const available = (registries ?? []).filter((r) => r.namespace === namespace)
  const selected = new Set(value.map((r) => r.name))
  const missing = value.filter((r) => !available.some((a) => a.name === r.name))

  function toggle(name: string, on: boolean) {
    onChange(on ? [...value, { name }] : value.filter((r) => r.name !== name))
  }

  return (
    <div className={styles.fieldGroup}>
      {!unlabeled && <span className={styles.label}>Registries</span>}
      {registries === null ? (
        <span className={styles.hint}>Loading Registries…</span>
      ) : available.length === 0 && missing.length === 0 ? (
        <span className={styles.hint}>
          No Registries in <code>{namespace}</code>. Without one, Kokumi accesses {owns} anonymously.
        </span>
      ) : (
        <>
          {available.map((r) => (
            <label key={r.name} className={styles.checkRow}>
              <input
                type="checkbox"
                checked={selected.has(r.name)}
                disabled={disabled}
                onChange={(e) => toggle(r.name, e.target.checked)}
              />
              <span>{r.name}</span>
              <span className={styles.hint}>
                {stripOCIScheme(r.url)}
                {r.secretRef ? ` · Secret ${r.secretRef}` : ' · no credentials'}
                {r.scheme === 'HTTP' ? ' · HTTP' : ''}
              </span>
            </label>
          ))}
          {missing.map((r) => (
            <div key={r.name} className={styles.checkRow}>
              <span className={styles.fieldError}>Registry {r.name} does not exist</span>
              <Button variant="ghost" size="sm" onClick={() => toggle(r.name, false)} disabled={disabled}>Remove</Button>
            </div>
          ))}
          <span className={styles.hint}>
            Each of {owns} is accessed through the selected Registry with the longest matching URL.
          </span>
        </>
      )}
    </div>
  )
}

interface RegistryMatchHintProps {
  /** The location; empty shows nothing. */
  url: string
  namespace: string
  registryRefs: RegistryRef[]
  operation: 'pull' | 'push'
}

/** Says which of the selected Registries Kokumi connects to url through. */
export function RegistryMatchHint({ url, namespace, registryRefs, operation }: RegistryMatchHintProps) {
  const registries = useRegistries()
  if (!url || registries === null) return null
  const match: Registry | undefined = matchRegistry(url, referencedRegistries(registries, namespace, registryRefs))
  if (!match) {
    return (
      <span className={styles.hint}>
        No selected Registry covers this URL: Kokumi {operation === 'push' ? 'pushes' : 'pulls'} without credentials.
      </span>
    )
  }
  return (
    <span className={styles.hint}>
      {operation === 'push' ? 'Pushed' : 'Pulled'} through Registry <strong>{match.name}</strong>
      {match.secretRef ? <> with the credentials in Secret <code>{match.secretRef}</code></> : ', without credentials'}.
    </span>
  )
}
