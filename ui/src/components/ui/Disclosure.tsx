import type { ReactNode } from 'react'
import styles from './Disclosure.module.css'

interface DisclosureProps {
  label: ReactNode
  /** Shown next to the label while collapsed. */
  summary?: ReactNode
  open: boolean
  onOpenChange: (open: boolean) => void
  /** section: uppercase form section header; inline: plain-text sub-toggle. */
  tone?: 'section' | 'inline'
  className?: string
  children: ReactNode
}

export function Disclosure({ label, summary, open, onOpenChange, tone = 'section', className, children }: DisclosureProps) {
  return (
    <div className={className}>
      <button
        type="button"
        className={`${styles.header} ${styles[tone]}`}
        aria-expanded={open}
        onClick={() => onOpenChange(!open)}
      >
        <span className={`${styles.chevron} ${open ? styles.chevronOpen : ''}`} aria-hidden>›</span>
        <span className={styles.label}>{label}</span>
        {!open && summary && <span className={styles.summary}>{summary}</span>}
      </button>
      {open && children}
    </div>
  )
}
