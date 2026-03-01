import { useState } from 'react'
import styles from './pages.module.css'

const ARGO_KEY = 'kokumi.argoCDBaseURL'

export default function Settings() {
  const [argoCDBase, setArgoCDBase] = useState<string>(
    () => localStorage.getItem(ARGO_KEY) ?? '',
  )
  const [saved, setSaved] = useState(false)
  const [urlError, setUrlError] = useState(false)

  function isValidURL(val: string): boolean {
    if (!val) return true // empty = clear setting, that's fine
    try {
      const u = new URL(val)
      return u.protocol === 'http:' || u.protocol === 'https:'
    } catch {
      return false
    }
  }

  function handleSave() {
    const trimmed = argoCDBase.trim().replace(/\/$/, '')
    if (!isValidURL(trimmed)) {
      setUrlError(true)
      return
    }
    localStorage.setItem(ARGO_KEY, trimmed)
    setArgoCDBase(trimmed)
    setUrlError(false)
    setSaved(true)
    setTimeout(() => setSaved(false), 2000)
  }

  return (
    <div className={styles.page}>
      <div className={styles.header}>
        <h1 className={styles.title}>Settings</h1>
        <p className={styles.subtitle}>Operator configuration and preferences</p>
      </div>

      <div className={styles.section}>
        <div className={styles.sectionHeader}>
          <span className={styles.sectionTitle}>Argo CD</span>
        </div>
        <div className={styles.sectionBody}>
          <div className={styles.settingsSection}>
            <div className={styles.fieldRow}>
              <label className={styles.fieldLabel} htmlFor="argoCDBase">
                Base URL
              </label>
              <input
                id="argoCDBase"
                className={styles.fieldInput}
                type="url"
                placeholder="https://argocd.example.com"
                value={argoCDBase}
                style={urlError ? { borderColor: '#c13a37' } : undefined}
                onChange={(e) => { setArgoCDBase(e.target.value); setSaved(false); setUrlError(false) }}
                onKeyDown={(e) => { if (e.key === 'Enter') handleSave() }}
              />
              <button
                style={{
                  padding: '7px 16px',
                  borderRadius: 6,
                  border: '1px solid rgba(49,54,56,0.2)',
                  background: 'var(--color-accent)',
                  color: '#fff',
                  fontFamily: 'inherit',
                  fontSize: '0.875rem',
                  fontWeight: 600,
                  cursor: 'pointer',
                }}
                onClick={handleSave}
              >
                Save
              </button>
              {saved && <span className={styles.savedMsg}>Saved ✓</span>}
              {urlError && (
                <span style={{ fontSize: '0.8rem', color: '#c13a37', fontWeight: 500 }}>
                  Must be a valid http:// or https:// URL
                </span>
              )}
            </div>
            <p style={{ fontSize: '0.8rem', color: 'var(--color-text-muted-light)', maxWidth: 520 }}>
              Used to generate deep links on the Servings page. Example:{' '}
              <code style={{ fontFamily: 'monospace' }}>https://argocd.example.com</code>
            </p>
          </div>
        </div>
      </div>
    </div>
  )
}

