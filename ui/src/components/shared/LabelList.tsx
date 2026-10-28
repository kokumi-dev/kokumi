import styles from './LabelList.module.css'

interface Props {
  labels?: Record<string, string>
  /** Shown without any label. */
  empty?: string
}

/** The labels of an object as key=value chips, sorted by key. */
export default function LabelList({ labels, empty = 'No labels' }: Props) {
  const entries = Object.entries(labels ?? {}).sort(([a], [b]) => a.localeCompare(b))
  if (entries.length === 0) return <span className={styles.empty}>{empty}</span>
  return (
    <span className={styles.list}>
      {entries.map(([k, v]) => (
        <span key={k} className={styles.chip} title={`${k}=${v}`}>
          {k}={v}
        </span>
      ))}
    </span>
  )
}
