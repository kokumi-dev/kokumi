import styles from './pages.module.css'

export default function Settings() {
  return (
    <div className={styles.page}>
      <div className={styles.header}>
        <h1 className={styles.title}>Settings</h1>
        <p className={styles.subtitle}>
          Operator configuration and preferences
        </p>
      </div>

      <div className={styles.section}>
        <div className={styles.sectionHeader}>
          <span className={styles.sectionTitle}>General</span>
        </div>
        <div className={styles.sectionBody}>
          <div className={styles.placeholder}>
            <svg className={styles.placeholderIcon} viewBox="0 0 40 40" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round">
              <circle cx="20" cy="20" r="5" />
              <path d="M20 3v4M20 33v4M3 20h4M33 20h4M7.2 7.2l2.8 2.8M30 30l2.8 2.8M7.2 32.8l2.8-2.8M30 10l2.8-2.8" />
            </svg>
            <span className={styles.placeholderText}>Settings coming soon</span>
          </div>
        </div>
      </div>
    </div>
  )
}
