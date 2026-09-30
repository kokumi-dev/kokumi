import styles from './Toggle.module.css'

interface Props {
  checked: boolean
  onChange: (checked: boolean) => void
  disabled?: boolean
  /** Text rendered next to the switch; also announces on focus. */
  label: string
  id?: string
}

/**
 * Toggle renders a palette-styled switch (accent color when checked) backed by
 * a real checkbox so keyboard focus and screen-reader semantics work for free.
 */
export default function Toggle({ checked, onChange, disabled, label, id }: Props) {
  return (
    <span className={styles.switch}>
      <input
        id={id}
        className={styles.input}
        type="checkbox"
        role="switch"
        checked={checked}
        disabled={disabled}
        aria-label={label}
        onChange={(e) => onChange(e.target.checked)}
      />
      <span className={styles.track} />
      <span className={styles.knob} />
    </span>
  )
}
