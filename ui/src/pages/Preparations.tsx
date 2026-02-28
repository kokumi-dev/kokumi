import styles from './pages.module.css'

export default function Preparations() {
  return (
    <div className={styles.page}>
      <div className={styles.header}>
        <h1 className={styles.title}>Preparations</h1>
        <p className={styles.subtitle}>
          Manage your Preparation custom resources
        </p>
      </div>

      <div className={styles.section}>
        <div className={styles.sectionHeader}>
          <span className={styles.sectionTitle}>All Preparations</span>
        </div>
        <div className={styles.sectionBody}>
          <div className={styles.placeholder}>
            <svg className={styles.placeholderIcon} viewBox="0 0 40 40" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round">
              <path d="M13 3v8a7 7 0 0 0 14 0V3" />
              <path d="M7 18h26L30 37H10L7 18Z" />
            </svg>
            <span className={styles.placeholderText}>No preparations found</span>
          </div>
        </div>
      </div>
    </div>
  )
}
