import { useMemo, useState } from 'react'
import type { Preparation } from '../api/types'
import { usePreparations } from '../hooks/usePreparations'
import ManifestModal from '../components/recipe/ManifestModal'
import styles from './pages.module.css'

function phaseBadgeClass(phase: string): string {
  const p = phase.toLowerCase()
  if (p === 'ready' || p === 'succeeded') return styles.badgeSuccess
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

export default function Preparations() {
  const preparations = usePreparations()
  const [query, setQuery] = useState('')
  const [manifestPrep, setManifestPrep] = useState<Preparation | null>(null)

  const filtered = useMemo(() => {
    if (!preparations) return []
    const q = query.trim().toLowerCase()
    if (!q) return preparations
    return preparations.filter(
      (p) =>
        p.name.toLowerCase().includes(q) ||
        p.namespace.toLowerCase().includes(q) ||
        p.order.toLowerCase().includes(q),
    )
  }, [preparations, query])

  return (
    <div className={styles.page}>
      <div className={styles.header}>
        <h1 className={styles.title}>Preparations</h1>
        <p className={styles.subtitle}>
          Rendered manifests produced by the controller for each Order revision
        </p>
      </div>

      <div className={styles.section}>
        <div className={styles.sectionHeader}>
          <span className={styles.sectionTitle}>All Preparations</span>
          <input
            className={styles.sectionSearch}
            type="search"
            placeholder="Filter…"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
        </div>
        <div>
          {preparations === null ? (
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
                <path d="M13 3v8a7 7 0 0 0 14 0V3" />
                <path d="M6 3h28M6 37h28M6 3v34M34 3v34" />
              </svg>
              <span className={styles.placeholderText}>No preparations found</span>
            </div>
          ) : (
            <table className={styles.table}>
              <thead className={styles.tableHead}>
                <tr>
                  <th></th>
                  <th>Phase</th>
                  <th>Name</th>
                  <th>Namespace</th>
                  <th>Order</th>
                  <th>Config Hash</th>
                  <th>Created</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((p) => (
                  <tr key={`${p.namespace}/${p.name}`} className={styles.tableRow}>
                    <td>
                      {p.isActive && <span className={styles.activePill}>active</span>}
                    </td>
                    <td>
                      <span className={`${styles.badge} ${phaseBadgeClass(p.phase)}`}>
                        <span className={styles.badgeDot} />
                        {p.phase}
                      </span>
                    </td>
                    <td className={styles.mono}>{p.name}</td>
                    <td>{p.namespace}</td>
                    <td>{p.order}</td>
                    <td>
                      <span
                        className={`${styles.mono} ${styles.truncate}`}
                        title={p.configHash}
                      >
                        {p.configHash.slice(0, 8)}
                      </span>
                    </td>
                    <td style={{ whiteSpace: 'nowrap' }}>{formatDate(p.createdAt)}</td>
                    <td>
                      <button
                        className={styles.iconBtn}
                        title="View rendered manifest"
                        onClick={() => setManifestPrep(p)}
                      >
                        {/* document icon */}
                        <svg width="15" height="15" viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
                          <path d="M4 2h8l4 4v12a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V3a1 1 0 0 1 1-1z" />
                          <path d="M12 2v4h4M7 9h6M7 12h6M7 15h4" />
                        </svg>
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>

      {manifestPrep && (
        <ManifestModal
          preparation={manifestPrep}
          onClose={() => setManifestPrep(null)}
        />
      )}
    </div>
  )
}
