import styles from './StatusIndicator.module.css'

type Tone = 'success' | 'warning' | 'error' | 'neutral'

function toneFor(state: string): Tone {
  switch (state.toLowerCase()) {
    case 'ready':
    case 'deployed':
    case 'resolved':
    case 'approved':
      return 'success'
    case 'pending':
    case 'processing':
    case 'deploying':
    case 'progressing':
      return 'warning'
    case 'failed':
    case 'error':
    case 'deploymentfailed':
      return 'error'
    default:
      return 'neutral'
  }
}

/** Compact status for lists: a coloured dot followed by the state text. */
export default function StatusIndicator({ state }: { state?: string }) {
  const label = state || 'Unknown'
  return (
    <span className={`${styles.status} ${styles[toneFor(label)]}`}>
      <span className={styles.dot} aria-hidden />
      {label}
    </span>
  )
}
