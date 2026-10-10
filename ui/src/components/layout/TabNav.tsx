import { NavLink } from 'react-router'
import Counter from '../shared/Counter'
import styles from './layout.module.css'

export interface TabItem {
  to: string
  label: string
  count?: number
  /** Match the path exactly (use for index tabs). */
  end?: boolean
}

export default function TabNav({ tabs }: { tabs: TabItem[] }) {
  return (
    <nav className={styles.tabs} aria-label="Tabs">
      {tabs.map((t) => (
        <NavLink
          key={t.to}
          to={t.to}
          end={t.end}
          className={({ isActive }) => `${styles.tab} ${isActive ? styles.tabActive : ''}`}
        >
          {t.label}
          <Counter count={t.count} />
        </NavLink>
      ))}
    </nav>
  )
}
