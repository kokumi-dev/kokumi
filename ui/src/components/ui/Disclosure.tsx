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
        <Chevron open={open} />
        <span className={styles.label}>{label}</span>
        {!open && summary && <span className={styles.summary}>{summary}</span>}
      </button>
      {open && children}
    </div>
  )
}

/**
 * The chevron of collapsible content; points down while open. It is drawn
 * centred on its box in both directions, so lines can attach to its centre.
 */
export function Chevron({ open }: { open: boolean }) {
  return (
    <svg
      className={`${styles.chevron} ${open ? styles.chevronOpen : ''}`}
      viewBox="0 0 16 16"
      width="14"
      height="14"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      <path d="M5.75 3.5 10.25 8 5.75 12.5" />
    </svg>
  )
}
