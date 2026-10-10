import { NavLink } from 'react-router'
import { Counter } from './Counter'
import styles from './Tabs.module.css'

interface TabItem {
  to: string
  label: string
  count?: number
  /** Match the path exactly (use for index tabs). */
  end?: boolean
}

/** Route-driven tabs. */
export function TabNav({ tabs }: { tabs: TabItem[] }) {
  return (
    <nav className={styles.tabs} aria-label="Tabs">
      {tabs.map((t) => (
        <NavLink
          key={t.to}
          to={t.to}
          end={t.end}
          className={({ isActive }) => `${styles.tab} ${isActive ? styles.tabActive : ''}`}
        >
          {({ isActive }) => (
            <>
              {t.label}
              <Counter count={t.count} active={isActive} />
            </>
          )}
        </NavLink>
      ))}
    </nav>
  )
}

interface TabButton<T extends string> {
  id: T
  label: string
}

interface TabButtonsProps<T extends string> {
  tabs: TabButton<T>[]
  active: T
  onSelect: (id: T) => void
}

/** In-page tabs that share the look of TabNav but switch local state instead of routes. */
export function TabButtons<T extends string>({ tabs, active, onSelect }: TabButtonsProps<T>) {
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
