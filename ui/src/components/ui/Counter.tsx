import styles from './Counter.module.css'

/** A count next to a title or tab; text such as "12 of 40" when a filter narrows it. */
export function Counter({ count, active }: { count?: number | string; active?: boolean }) {
  if (!count) return null
  return <span className={`${styles.counter} ${active ? styles.active : ''}`}>{count}</span>
}
