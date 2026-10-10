import styles from './SegmentedControl.module.css'

interface Segment<T extends string> {
  id: T
  label: string
}

interface SegmentedControlProps<T extends string> {
  segments: Segment<T>[]
  value: T
  onChange: (id: T) => void
  'aria-label'?: string
}

/** Mutually exclusive mode switch inside forms; use TabButtons for real tabs. */
export function SegmentedControl<T extends string>({ segments, value, onChange, ...rest }: SegmentedControlProps<T>) {
  return (
    <div className={styles.track} role="radiogroup" aria-label={rest['aria-label']}>
      {segments.map((s) => (
        <button
          key={s.id}
          type="button"
          role="radio"
          aria-checked={s.id === value}
          className={`${styles.item} ${s.id === value ? styles.active : ''}`}
          onClick={() => s.id !== value && onChange(s.id)}
        >
          {s.label}
        </button>
      ))}
    </div>
  )
}
