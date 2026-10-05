import type { ReactNode } from 'react'
import styles from './layout.module.css'

interface Props {
  title?: ReactNode
  description?: ReactNode
  actions?: ReactNode
  /** Drop body padding, e.g. for edge-to-edge tables. */
  flush?: boolean
  danger?: boolean
  children: ReactNode
}

export default function Section({ title, description, actions, flush, danger, children }: Props) {
  return (
    <section className={`${styles.section} ${danger ? styles.sectionDanger : ''}`}>
      {(title || actions) && (
        <div className={styles.sectionHeader}>
          <div className={styles.sectionHeading}>
            {title && <span className={styles.sectionTitle}>{title}</span>}
            {description && <span className={styles.sectionDescription}>{description}</span>}
          </div>
          {actions && <div className={styles.sectionActions}>{actions}</div>}
        </div>
      )}
      <div className={flush ? undefined : styles.sectionBody}>{children}</div>
    </section>
  )
}
