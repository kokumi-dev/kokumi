import { Link } from 'react-router'
import type { ReactNode } from 'react'
import styles from './layout.module.css'

export interface Crumb {
  label: string
  to?: string
}

interface Props {
  title: ReactNode
  subtitle?: ReactNode
  breadcrumbs?: Crumb[]
  /** Rendered next to the title, typically a state Badge. */
  badge?: ReactNode
  actions?: ReactNode
}

export default function PageHeader({ title, subtitle, breadcrumbs, badge, actions }: Props) {
  return (
    <header className={styles.header}>
      {breadcrumbs && breadcrumbs.length > 0 && (
        <nav className={styles.breadcrumbs} aria-label="Breadcrumb">
          {breadcrumbs.map((c, i) => (
            <span key={`${c.label}-${i}`} style={{ display: 'contents' }}>
              {i > 0 && <span className={styles.breadcrumbSep}>/</span>}
              {c.to ? (
                <Link className={styles.breadcrumbLink} to={c.to}>{c.label}</Link>
              ) : (
                <span>{c.label}</span>
              )}
            </span>
          ))}
        </nav>
      )}
      <div className={styles.headerRow}>
        <div>
          <div className={styles.titleRow}>
            <h1 className={styles.title}>{title}</h1>
            {badge}
          </div>
          {subtitle && <p className={styles.subtitle}>{subtitle}</p>}
        </div>
        {actions && <div className={styles.actions}>{actions}</div>}
      </div>
    </header>
  )
}
