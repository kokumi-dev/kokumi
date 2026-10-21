import { useId } from 'react'
import { useOrders } from '../../hooks/useOrders'
import styles from '../shared/Form.module.css'

interface EnvironmentFieldProps {
  namespace: string
  /** The Product of the Order; the environments of its other variants are offered. */
  product: string
  /** The name of the Order, whose own environment is not offered twice. */
  self: string
  value: string
  onChange: (environment: string) => void
}

/** A Kubernetes label value: at most 63 characters, alphanumerics with -, _ and . inside. */
const labelValue = /^([A-Za-z0-9]([-A-Za-z0-9_.]{0,61}[A-Za-z0-9])?)?$/

/**
 * EnvironmentField sets the environment of the pipeline an Order belongs
 * to, the label delivery.kokumi.dev/environment. The Orders of a Product that
 * share it are drawn as one column of its pipeline.
 */
export default function EnvironmentField({ namespace, product, self, value, onChange }: EnvironmentFieldProps) {
  const listId = useId()
  const orders = useOrders()
  const known = [
    ...new Set(
      (orders ?? [])
        .filter((o) => o.namespace === namespace && o.productRef.name === product && o.name !== self)
        .flatMap((o) => (o.environment ? [o.environment] : [])),
    ),
  ].sort()
  const valid = labelValue.test(value)

  return (
    <div className={styles.fieldGroup}>
      <label className={styles.label}>Environment</label>
      <input
        className={styles.input}
        value={value}
        onChange={(e) => onChange(e.target.value.trim())}
        placeholder="int"
        list={listId}
        aria-invalid={!valid}
      />
      <datalist id={listId}>
        {known.map((e) => (
          <option key={e} value={e} />
        ))}
      </datalist>
      <span className={styles.hint}>
        {valid
          ? 'Orders of the Product with the same environment, such as the regions of int, are drawn as one column of its pipeline. Empty keeps the Order a column of its own.'
          : 'Use at most 63 letters, digits, -, _ and ., starting and ending with a letter or digit.'}
      </span>
    </div>
  )
}
