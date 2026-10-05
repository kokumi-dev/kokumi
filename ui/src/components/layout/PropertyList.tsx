import type { ReactNode } from 'react'
import styles from './layout.module.css'

export interface Property {
  label: string
  value: ReactNode
  /** Skip the row entirely when false. */
  show?: boolean
}

export default function PropertyList({ items }: { items: Property[] }) {
  return (
    <dl className={styles.propertyList}>
      {items
        .filter((p) => p.show !== false)
        .map((p) => (
          <div key={p.label} style={{ display: 'contents' }}>
            <dt className={styles.propertyLabel}>{p.label}</dt>
            <dd className={styles.propertyValue}>{p.value}</dd>
          </div>
        ))}
    </dl>
  )
}
