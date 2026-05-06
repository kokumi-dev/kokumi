import styles from './Badge.module.css'

const classForState: Record<string, string> = {
  Ready: styles.ready,
  Deployed: styles.deployed,
  Pending: styles.pending,
  Processing: styles.processing,
  Deploying: styles.deploying,
  Failed: styles.failed,
}

interface Props {
  state: string
}

/** Renders a coloured state pill for an Order, Preparation, or Serving. */
export default function Badge({ state }: Props) {
  const cls = classForState[state] ?? styles.unknown
  return <span className={`${styles.badge} ${cls}`}>{state || '—'}</span>
}
