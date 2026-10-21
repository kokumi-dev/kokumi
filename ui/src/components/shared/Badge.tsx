import styles from './Badge.module.css'
import type { Tone } from './tone'

const classForTone: Record<Tone, string> = {
  success: styles.success,
  warning: styles.warning,
  danger: styles.danger,
  accent: styles.accent,
  neutral: styles.neutral,
}

interface Props {
  state: string
  /** Neutral unless given; stateTone gives the tone of an object's state. */
  tone?: Tone
  title?: string
}

/** Renders a coloured pill, e.g. the state of an Order, Preparation, or Serving. */
export default function Badge({ state, tone = 'neutral', title }: Props) {
  return <span className={`${styles.badge} ${classForTone[tone]}`} title={title}>{state || '—'}</span>
}
