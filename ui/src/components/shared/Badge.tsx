import styles from './Badge.module.css'

const classForState: Record<string, string> = {
  Ready: styles.ready,
  Deployed: styles.deployed,
  Pending: styles.pending,
  Processing: styles.processing,
  Deploying: styles.deploying,
  Waiting: styles.waiting,
  Deleting: styles.deleting,
  Failed: styles.failed,
}

/** Colours a badge independently of its text, e.g. for a severity. */
export type BadgeTone = 'success' | 'warning' | 'danger' | 'neutral'

const classForTone: Record<BadgeTone, string> = {
  success: styles.ready,
  warning: styles.pending,
  danger: styles.failed,
  neutral: styles.unknown,
}

interface Props {
  state: string
  /** Overrides the colour of the state. */
  tone?: BadgeTone
  title?: string
}

/** Renders a coloured state pill for an Order, Preparation, or Serving. */
export default function Badge({ state, tone, title }: Props) {
  const cls = tone ? classForTone[tone] : classForState[state] ?? styles.unknown
  return <span className={`${styles.badge} ${cls}`} title={title}>{state || '—'}</span>
}
