import styles from './layout.module.css'

export interface TabButton<T extends string> {
  id: T
  label: string
}

interface Props<T extends string> {
  tabs: TabButton<T>[]
  active: T
  onSelect: (id: T) => void
}

/** In-page tabs that share the look of TabNav but switch local state instead of routes. */
export default function TabButtons<T extends string>({ tabs, active, onSelect }: Props<T>) {
  return (
    <div className={styles.tabs} role="tablist">
      {tabs.map((t) => (
        <button
          key={t.id}
          type="button"
          role="tab"
          aria-selected={t.id === active}
          className={`${styles.tab} ${styles.tabButton} ${t.id === active ? styles.tabActive : ''}`}
          onClick={() => t.id !== active && onSelect(t.id)}
        >
          {t.label}
        </button>
      ))}
    </div>
  )
}
