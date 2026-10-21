import styles from './StatusIndicator.module.css'
import type { Tone } from './tone'

/** Compact status for lists: a dot in the colour of tone followed by the state text. */
export default function StatusIndicator({ state, tone }: { state?: string; tone: Tone }) {
  return (
    <span className={`${styles.status} ${styles[tone]}`}>
      <span className={styles.dot} aria-hidden />
      {state || 'Unknown'}
    </span>
  )
}
