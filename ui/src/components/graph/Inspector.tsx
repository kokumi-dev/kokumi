import type { ReactNode } from 'react'
import { CloseIcon, IconButton } from '../ui'
import { StatusMark, type Status } from './parts'
import styles from './Inspector.module.css'

interface Props {
  /** Names the details for assistive technology, e.g. "Signature details". */
  label: string
  icon: ReactNode
  title: ReactNode
  status?: Status
  /** Shown before the close button, e.g. a link that opens the node's page. */
  actions?: ReactNode
  onClose: () => void
  children: ReactNode
}

/**
 * Inspector shows the details of the selected node of a graph beside it. It
 * carries data-graph-details, so that a click inside it does not close it.
 */
export function Inspector({ label, icon, title, status, actions, onClose, children }: Props) {
  return (
    <aside className={styles.inspector} aria-label={label} data-graph-details>
      <div className={styles.head}>
        {icon}
        <div className={styles.title}>
          <span className={styles.titleText}>{title}</span>
          {status && <StatusMark status={status} />}
        </div>
        {actions}
        <IconButton aria-label="Close details" onClick={onClose}><CloseIcon /></IconButton>
      </div>
      {children}
    </aside>
  )
}

/** Sentences that explain the node. */
export function Prose({ children }: { children: ReactNode }) {
  return <div className={styles.prose}>{children}</div>
}

export function Facts({ children }: { children: ReactNode }) {
  return <dl className={styles.facts}>{children}</dl>
}

export function Fact({ label, children }: { label: ReactNode; children: ReactNode }) {
  return (
    <div className={styles.fact}>
      <dt className={styles.factLabel}>{label}</dt>
      <dd className={styles.factValue}>{children}</dd>
    </div>
  )
}

/** A label like that of a fact, for content outside a list of facts. */
export function FactLabel({ children }: { children: ReactNode }) {
  return <span className={styles.factLabel}>{children}</span>
}

/** Collapsible extras below a rule, such as annotations or values. */
export function Disclosures({ children }: { children: ReactNode }) {
  return <div className={styles.disclosures}>{children}</div>
}

/** A long identifier that wraps anywhere, such as a digest. */
export function Mono({ children }: { children: ReactNode }) {
  return <span className={styles.mono}>{children}</span>
}

/** Preformatted text, such as JSON or YAML. */
export function Code({ children }: { children: ReactNode }) {
  return <pre className={styles.code}>{children}</pre>
}
