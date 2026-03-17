import { useMemo, useState } from 'react'
import { useServings } from '../hooks/useServings'
import styles from './pages.module.css'

function phaseBadgeClass(phase: string): string {
  const p = phase.toLowerCase()
  if (p === 'deployed' || p === 'ready' || p === 'succeeded') return styles.badgeSuccess
  if (p === 'failed' || p === 'error') return styles.badgeError
  return styles.badgeWarning
}

function formatDate(iso?: string): string {
  if (!iso) return '—'
  return new Date(iso).toLocaleString(undefined, {
    dateStyle: 'short',
    timeStyle: 'short',
  })
}

export default function Servings() {
  const servings = useServings()
  const [query, setQuery] = useState('')

  const argoCDBase = useMemo(() => {
    const raw = (localStorage.getItem('kokumi.argoCDBaseURL') ?? '').trim().replace(/\/$/, '')
    if (!raw) return ''
    try {
      const u = new URL(raw)
      if (u.protocol !== 'http:' && u.protocol !== 'https:') return ''
      return raw
    } catch {
      return ''
    }
  }, [])


  const filtered = useMemo(() => {
    if (!servings) return []
    const q = query.trim().toLowerCase()
    if (!q) return servings
    return servings.filter(
      (s) =>
        s.name.toLowerCase().includes(q) ||
        s.namespace.toLowerCase().includes(q) ||
        s.order.toLowerCase().includes(q),
    )
  }, [servings, query])

  return (
    <div className={styles.page}>
      <div className={styles.header}>
        <h1 className={styles.title}>Servings</h1>
        <p className={styles.subtitle}>
          Active deployments managed by the controller for each Order
        </p>
      </div>

      <div className={styles.section}>
        <div className={styles.sectionHeader}>
          <span className={styles.sectionTitle}>All Servings</span>
          <input
            className={styles.sectionSearch}
            type="search"
            placeholder="Filter…"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
        </div>
        <div>
          {servings === null ? (
            <div className={styles.placeholder}>
              <span className={styles.placeholderText}>Loading…</span>
            </div>
          ) : filtered.length === 0 ? (
            <div className={styles.placeholder}>
              <svg
                className={styles.placeholderIcon}
                viewBox="0 0 40 40"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.5"
                strokeLinecap="round"
              >
                <circle cx="20" cy="21" r="13" />
                <path d="M7 21h26" />
                <path d="M20 4v5" />
              </svg>
              <span className={styles.placeholderText}>No servings found</span>
            </div>
          ) : (
            <table className={styles.table}>
              <thead className={styles.tableHead}>
                <tr>
                  <th>Phase</th>
                  <th>Name</th>
                  <th>Namespace</th>
                  <th>Order</th>
                  <th>Desired Prep</th>
                  <th>Observed Prep</th>
                  <th>Policy</th>
                  <th>Created</th>
                  <th>Argo CD</th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((s) => {
                  const argoURL = argoCDBase
                    ? `${argoCDBase}/applications/argocd/${s.name}`
                    : null

                  return (
                    <tr key={`${s.namespace}/${s.name}`} className={styles.tableRow}>
                      <td>
                        <span className={`${styles.badge} ${phaseBadgeClass(s.phase)}`}>
                          <span className={styles.badgeDot} />
                          {s.phase}
                        </span>
                      </td>
                      <td className={styles.mono}>{s.name}</td>
                      <td>{s.namespace}</td>
                      <td>{s.order}</td>
                      <td>
                        <span
                          className={`${styles.mono} ${styles.truncate}`}
                          title={s.desiredPreparation}
                        >
                          {s.desiredPreparation.slice(0, 12)}
                        </span>
                      </td>
                      <td>
                        {s.observedPreparation ? (
                          <span
                            className={`${styles.mono} ${styles.truncate}`}
                            title={s.observedPreparation}
                          >
                            {s.observedPreparation.slice(0, 12)}
                          </span>
                        ) : (
                          <span style={{ color: 'var(--color-text-muted-light)' }}>—</span>
                        )}
                      </td>
                      <td>
                        <span className={styles.policyPill}>{s.preparationPolicy}</span>
                      </td>
                      <td style={{ whiteSpace: 'nowrap' }}>{formatDate(s.createdAt)}</td>
                      <td>
                        {argoURL ? (
                          <a
                            href={argoURL}
                            target="_blank"
                            rel="noopener noreferrer"
                            className={styles.iconBtn}
                            title={`Open in Argo CD — ${s.name}`}
                          >
                            {/* external link icon */}
                            <svg width="14" height="14" viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round">
                              <path d="M9 3H4a1 1 0 0 0-1 1v12a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-5" />
                              <path d="M13 3h4v4" />
                              <path d="M20 3 9 14" />
                            </svg>
                          </a>
                        ) : (
                          <span
                            className={`${styles.iconBtn} ${styles.iconBtnDisabled}`}
                            title="Set Argo CD URL in Settings"
                          >
                            <svg width="14" height="14" viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round">
                              <path d="M9 3H4a1 1 0 0 0-1 1v12a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-5" />
                              <path d="M13 3h4v4" />
                              <path d="M20 3 9 14" />
                            </svg>
                          </span>
                        )}
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          )}
        </div>
      </div>
    </div>
  )
}

