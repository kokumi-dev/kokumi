import { useEffect, useId } from 'react'
import type { Environment } from '../../api/types'
import { useEnvironments } from '../../hooks/useEnvironments'
import { formatDuration } from '../../utils/format'
import styles from '../shared/Form.module.css'

interface EnvironmentPickerProps {
  namespace: string
  /** The name of the Environment as set; empty until one is picked. */
  value: string
  onChange: (environment: string) => void
  /**
   * Picks the only Environment of the namespace while none is picked yet;
   * it does not count as a change of the form.
   */
  onPreselect: (environment: string) => void
  /** Set once the Order exists: its Environment cannot change. */
  locked: boolean
}

/**
 * EnvironmentPicker picks the Environment an Order deploys to among those of
 * its namespace. It is required, picked once when the Order is created, and
 * preselected when the namespace has only one.
 */
export default function EnvironmentPicker({ namespace, value, onChange, onPreselect, locked }: EnvironmentPickerProps) {
  const id = useId()
  const environments = useEnvironments()?.filter((e) => e.namespace === namespace)
  const environment = environments?.find((e) => e.name === value)
  const only = environments?.length === 1 ? environments[0].name : undefined

  useEffect(() => {
    if (!locked && !value && only) onPreselect(only)
  }, [locked, value, only, onPreselect])

  const named = (name: string) => <code>{name}</code>

  return (
    <div className={styles.fieldGroup}>
      <label className={styles.label} htmlFor={id}>Environment</label>
      {locked ? (
        <input id={id} className={`${styles.input} ${styles.inputDisabled}`} value={value} readOnly />
      ) : (
        <select id={id} className={styles.input} value={value} onChange={(e) => onChange(e.target.value)} required>
          <option value="">— select an Environment —</option>
          {value && environments && !environment && <option value={value}>{value} (not found)</option>}
          {environments?.map((e) => (
            <option key={e.name} value={e.name}>{e.description ? `${e.name}: ${e.description}` : e.name}</option>
          ))}
        </select>
      )}
      {locked ? (
        <span className={styles.hint}>
          Deploys to the Clusters of {named(value)}. The Environment of an Order cannot change: create a new Order to
          deploy to another one.
        </span>
      ) : environments?.length === 0 ? (
        <span className={styles.hint}>
          No Environment in namespace <code>{namespace || '—'}</code> yet: an admin creates one, or applies the example{' '}
          <code>local</code> to the namespace.
        </span>
      ) : value && environments && !environment ? (
        <span className={styles.fieldError}>Environment {value} is not in namespace {namespace}: the Order would not deploy.</span>
      ) : (
        <span className={styles.hint}>
          {value ? <>Deploys to the Clusters of {named(value)}, one Argo CD Application each.</> : 'The Environment of its namespace the Order deploys to.'}{' '}
          It is picked once, when the Order is created.
        </span>
      )}
      {environment && <Requirements environment={environment} />}
    </div>
  )
}

/**
 * Requirements says which Environments an Order of the Environment must run
 * after. Whether the Order meets them is for its Serving to report.
 */
function Requirements({ environment }: { environment: Environment }) {
  const required = environment.promotion?.requireAfter ?? []
  if (required.length === 0) return null
  const each = required.map(
    (r) => `an Order of ${r.environment}${r.minSoakSeconds ? `, with a soak of at least ${formatDuration(r.minSoakSeconds)}` : ''}`,
  )
  return (
    <span className={styles.hint}>
      {environment.name} requires running after {each.join('; and after ')}. Unless the Order names{' '}
      {required.length === 1 ? 'one' : 'one of each'} under Runs after, its Serving reports PipelineRequired and does
      not deploy.
    </span>
  )
}
