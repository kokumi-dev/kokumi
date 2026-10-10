import { Link } from 'react-router'
import type { ReactNode } from 'react'
import styles from './layout.module.css'

interface Props {
  to: string
  name: string
  /** Muted second line, e.g. the namespace. */
  secondary?: string
  /** Optional flag rendered next to the name, e.g. an "Active" label. */
  label?: ReactNode
}

/** First cell of every resource list: linked name with a muted second line. */
export default function NameCell({ to, name, secondary, label }: Props) {
  return (
    <>
      <div className={styles.cellRow}>
        <Link className={styles.primaryLink} to={to} title={name}>{name}</Link>
        {label}
      </div>
      {secondary && <span className={styles.secondaryText} title={secondary}>{secondary}</span>}
    </>
  )
}
