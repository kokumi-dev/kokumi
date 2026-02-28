import styles from './pages.module.css'

export default function Servings() {
  return (
    <div className={styles.page}>
      <div className={styles.header}>
        <h1 className={styles.title}>Servings</h1>
        <p className={styles.subtitle}>
          Manage your Serving custom resources
        </p>
      </div>

      <div className={styles.section}>
        <div className={styles.sectionHeader}>
          <span className={styles.sectionTitle}>All Servings</span>
        </div>
        <div className={styles.sectionBody}>
          <div className={styles.placeholder}>
            <svg className={styles.placeholderIcon} viewBox="0 0 40 40" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round">
              <circle cx="20" cy="21" r="13" />
              <path d="M7 21h26" />
              <path d="M20 4v5" />
            </svg>
            <span className={styles.placeholderText}>No servings found</span>
          </div>
        </div>
      </div>
    </div>
  )
}
