import type { Tone } from '../shared/tone'
import styles from './ToggleChip.module.css'

interface ToggleChipProps {
  label: string
  /** Shown after the label, e.g. how many rows the chip selects. */
  count?: number
  pressed: boolean
  onToggle: () => void
  /** Colours a dot before the label, e.g. by severity. */
  tone?: Tone
}

/** A filter that is on or off; several chips together select any of their values. */
export function ToggleChip({ label, count, pressed, onToggle, tone }: ToggleChipProps) {
  return (
    <button
      type="button"
      className={`${styles.chip} ${pressed ? styles.pressed : ''}`}
      aria-pressed={pressed}
      aria-label={count === undefined ? undefined : `${label} ${count}`}
      onClick={onToggle}
    >
      {tone && <span className={`${styles.dot} ${styles[tone]}`} aria-hidden />}
      {label}
      {count !== undefined && <span className={styles.count}>{count}</span>}
    </button>
  )
}
