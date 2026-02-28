import { useResourceCounts } from '../hooks/useResourceCounts'
import styles from './pages.module.css'

interface Props {
  operatorName?: string
  operatorVersion?: string
}

export default function Dashboard({ operatorName, operatorVersion }: Props) {
  const counts = useResourceCounts()

  return (
    <div className={styles.page}>
      <div className={styles.header}>
        <h1 className={styles.title}>Dashboard</h1>
        <p className={styles.subtitle}>
          Overview of your {operatorName ?? 'kokumi'} operator deployment
        </p>
      </div>

      <div className={styles.statsGrid}>
        <div className={styles.statCard}>
          <span className={styles.statLabel}>Operator Version</span>
          <span className={`${styles.statValue} ${styles.statValueAccent}`}>
            {operatorVersion ?? '—'}
          </span>
        </div>
        <div className={styles.statCard}>
          <span className={styles.statLabel}>Recipes</span>
          <span className={styles.statValue}>{counts?.recipes ?? '—'}</span>
        </div>
        <div className={styles.statCard}>
          <span className={styles.statLabel}>Preparations</span>
          <span className={styles.statValue}>{counts?.preparations ?? '—'}</span>
        </div>
        <div className={styles.statCard}>
          <span className={styles.statLabel}>Servings</span>
          <span className={styles.statValue}>{counts?.servings ?? '—'}</span>
        </div>
      </div>

      <div className={styles.section}>
        <div className={styles.sectionHeader}>
          <span className={styles.sectionTitle}>Operator Status</span>
          <span className={`${styles.badge} ${styles.badgeSuccess}`}>
            <span className={styles.badgeDot} />
            Online
          </span>
        </div>
        <div className={styles.sectionBody}>
          <div className={styles.placeholder}>
            <svg className={styles.placeholderIcon} viewBox="0 0 40 40" fill="none" stroke="currentColor" strokeWidth="1.5">
              <rect x="2" y="2" width="16" height="16" rx="2" />
              <rect x="22" y="2" width="16" height="16" rx="2" />
              <rect x="2" y="22" width="16" height="16" rx="2" />
              <rect x="22" y="22" width="16" height="16" rx="2" />
            </svg>
            <span className={styles.placeholderText}>
              Resource metrics coming soon
            </span>
          </div>
        </div>
      </div>
    </div>
  )
}
