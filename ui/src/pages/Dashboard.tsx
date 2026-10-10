import OpenPromotions from '../components/dashboard/OpenPromotions'
import PageHeader from '../components/layout/PageHeader'
import Section from '../components/layout/Section'
import { useResourceCounts } from '../hooks/useResourceCounts'
import { useAppInfo } from '../appContext'
import layout from '../components/layout/layout.module.css'
import styles from './pages.module.css'

export default function Dashboard() {
  const { operatorName, operatorVersion } = useAppInfo()
  const counts = useResourceCounts()

  return (
    <div className={layout.page}>
      <PageHeader
        title="Dashboard"
        subtitle={`Overview of your ${operatorName ?? 'kokumi'} operator deployment`}
      />

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

      <Section title="Open Promotions" description="Orders whose latest Preparation is not live yet" flush>
        <OpenPromotions />
      </Section>
    </div>
  )
}
