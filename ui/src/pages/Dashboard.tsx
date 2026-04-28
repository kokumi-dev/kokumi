import OpenPromotions from '../components/dashboard/OpenPromotions'
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
          <span className={styles.statLabel}>Orders</span>
          <span className={styles.statValue}>{counts?.orders ?? '—'}</span>
        </div>
        <div className={styles.statCard}>
          <span className={styles.statLabel}>Menus</span>
          <span className={styles.statValue}>{counts?.menus ?? '—'}</span>
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
          <span className={styles.sectionTitle}>Open Promotions</span>
        </div>
        <div>
          <OpenPromotions />
        </div>
      </div>
    </div>
  )
}
