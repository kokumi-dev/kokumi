import styles from './pages.module.css'

export default function Recipes() {
  return (
    <div className={styles.page}>
      <div className={styles.header}>
        <h1 className={styles.title}>Recipes</h1>
        <p className={styles.subtitle}>
          Manage your Recipe custom resources
        </p>
      </div>

      <div className={styles.section}>
        <div className={styles.sectionHeader}>
          <span className={styles.sectionTitle}>All Recipes</span>
        </div>
        <div className={styles.sectionBody}>
          <div className={styles.placeholder}>
            <svg className={styles.placeholderIcon} viewBox="0 0 40 40" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round">
              <path d="M10 4v32M10 14h14a6 6 0 0 1 0 12H10" />
            </svg>
            <span className={styles.placeholderText}>No recipes found</span>
          </div>
        </div>
      </div>
    </div>
  )
}
