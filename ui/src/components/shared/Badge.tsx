import styles from './Badge.module.css'

const classForPhase: Record<string, string> = {
  Ready: styles.ready,
  Deployed: styles.deployed,
  Pending: styles.pending,
  Processing: styles.processing,
  Deploying: styles.deploying,
  Failed: styles.failed,
}

interface Props {
  phase: string
}

/** Renders a coloured phase/status pill for a Order, Preparation, or Serving. */
export default function Badge({ phase }: Props) {
  const cls = classForPhase[phase] ?? styles.unknown
  return <span className={`${styles.badge} ${cls}`}>{phase || '—'}</span>
}
