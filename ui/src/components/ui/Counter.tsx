import styles from './Counter.module.css'

export function Counter({ count, active }: { count?: number; active?: boolean }) {
  if (!count) return null
  return <span className={`${styles.counter} ${active ? styles.active : ''}`}>{count}</span>
}
