import type { ButtonHTMLAttributes, HTMLAttributes, ReactNode } from 'react'
import styles from './parts.module.css'

/** The colour of an icon tile. */
export type IconTone = 'accent' | 'success' | 'warning' | 'muted'

/** An icon in a soft tile. Its paths are drawn on a 16×16 grid. */
export function NodeIcon({ tone, size = 'md', children }: { tone: IconTone; size?: 'md' | 'lg'; children: ReactNode }) {
  return (
    <span className={`${styles.icon} ${styles[`icon_${size}`]} ${styles[`tone_${tone}`]}`} aria-hidden>
      <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
        {children}
      </svg>
    </span>
  )
}

export type Tone = 'ok' | 'neutral' | 'bad'

export interface Status {
  tone: Tone
  label: string
}

/** A status: a small mark and a few words. */
export function StatusMark({ status, quiet }: { status: Status; quiet?: boolean }) {
  return (
    <span className={`${styles.status} ${styles[`status_${status.tone}`]} ${quiet ? styles.statusQuiet : ''}`}>
      <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
        {status.tone === 'ok' && <path d="m4 8.5 2.75 2.75L12 5.5" />}
        {status.tone === 'bad' && <path d="m5 5 6 6M11 5l-6 6" />}
        {status.tone === 'neutral' && <circle cx="8" cy="8" r="2" fill="currentColor" stroke="none" />}
      </svg>
      <span className={styles.statusLabel}>{status.label}</span>
    </span>
  )
}

/** The icon, title and status of a node, the status below the title and extra after the status. */
export function NodeSummary({ icon, title, status, extra }: { icon: ReactNode; title: ReactNode; status: Status; extra?: ReactNode }) {
  return (
    <>
      {icon}
      <span className={styles.summaryText}>
        <span className={styles.summaryTitle}>{title}</span>
        <span className={styles.summaryLine}>
          <StatusMark status={status} quiet />
          {extra}
        </span>
      </span>
    </>
  )
}

interface NodeButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  /** The key of the node it selects. */
  nodeKey: string
  selected: boolean
}

/**
 * Selects a node of a graph. It carries data-graph-node, so that a click on
 * it does not count as a click outside the details.
 */
export function NodeButton({ nodeKey, selected, className, ...rest }: NodeButtonProps) {
  return (
    // eslint-disable-next-line no-restricted-syntax -- selects a node of a diagram, not an action button
    <button
      type="button"
      aria-pressed={selected}
      data-graph-node={nodeKey}
      className={`${styles.nodeButton} ${className ?? ''}`}
      {...rest}
    />
  )
}

/** A node drawn as a card that selects it. */
export function NodeCard({ selected, className, ...rest }: NodeButtonProps) {
  return <NodeButton selected={selected} className={`${styles.card} ${selected ? styles.selected : ''} ${className ?? ''}`} {...rest} />
}

/** A node drawn as a card that is not selectable. */
export function StaticCard({ className, ...rest }: HTMLAttributes<HTMLDivElement>) {
  return <div className={`${styles.card} ${styles.staticCard} ${className ?? ''}`} {...rest} />
}

/** The shape of a chip: plain, accent for something that wants attention, faded for something that no longer holds. */
export type ChipTone = 'plain' | 'accent' | 'faded'

interface ChipProps {
  icon?: ReactNode
  tone?: ChipTone
  mono?: boolean
  title?: string
  className?: string
  children: ReactNode
}

/** A short label in a pill, such as a tag, a version or a signer. */
export function Chip({ icon, tone = 'plain', mono, title, className, children }: ChipProps) {
  return (
    <span className={`${styles.chip} ${styles[`chip_${tone}`]} ${mono ? styles.mono : ''} ${className ?? ''}`} title={title}>
      {icon}
      {children}
    </span>
  )
}

/** The icon of a tag: a name that can move. */
export function TagIcon() {
  return (
    <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      <path d="M2.25 2.25h5l6.5 6.5-5 5-6.5-6.5v-5Z" />
      <path d="M5.25 5.25h.01" />
    </svg>
  )
}
