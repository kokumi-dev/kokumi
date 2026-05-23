import type { Pantry } from '../../api/types'
import Badge from '../shared/Badge'
import Btn from '../shared/Btn'
import styles from './PantryDetail.module.css'

interface Props {
  pantry: Pantry
  onClose: () => void
  onEdit: (pantry: Pantry) => void
  onDelete: (pantry: Pantry) => void
}

export default function PantryDetail({ pantry, onClose, onEdit, onDelete }: Props) {
  return (
    <>
      <div className={styles.backdrop} onClick={onClose} />
      <div className={styles.panel}>
        {/* Header */}
        <div className={styles.header}>
          <div className={styles.headerLeft}>
            <span className={styles.title}>{pantry.name}</span>
            <span className={styles.subtitle}>{pantry.namespace}</span>
          </div>
          <div className={styles.headerActions}>
            <Badge state={pantry.state ?? ''} />
            <Btn variant="secondary" size="sm" onClick={() => onEdit(pantry)}>Edit</Btn>
            <Btn variant="danger" size="sm" onClick={() => onDelete(pantry)}>Delete</Btn>
            <button className={styles.closeBtn} onClick={onClose} aria-label="Close panel">
              <svg viewBox="0 0 14 14" width="14" height="14" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
                <path d="M2 2l10 10M12 2L2 12" />
              </svg>
            </button>
          </div>
        </div>

        {/* Body */}
        <div className={styles.body}>
          {/* Spec */}
          <div className={styles.section}>
            <span className={styles.sectionTitle}>Spec</span>
            <div className={styles.specGrid}>
              <span className={styles.specKey}>URL</span>
              <span className={styles.specValue}>{pantry.url}</span>
              {pantry.secretRef && (
                <>
                  <span className={styles.specKey}>Secret</span>
                  <span className={styles.specValue}>{pantry.secretRef}</span>
                </>
              )}
              {pantry.description && (
                <>
                  <span className={styles.specKey}>Description</span>
                  <span className={styles.specValue}>{pantry.description}</span>
                </>
              )}
            </div>
          </div>

          {/* Conditions */}
          {pantry.conditions && pantry.conditions.length > 0 && (
            <div className={styles.section}>
              <span className={styles.sectionTitle}>Conditions</span>
              <div className={styles.conditionList}>
                {pantry.conditions.map((c) => (
                  <div key={c.type} className={styles.condition}>
                    <div className={styles.conditionHeader}>
                      <span className={styles.conditionType}>{c.type}</span>
                      <span
                        className={`${styles.conditionStatus} ${
                          c.status === 'True' ? styles.conditionStatusTrue : styles.conditionStatusFalse
                        }`}
                      >
                        {c.status}
                      </span>
                      {c.reason && <span className={styles.conditionMessage}>{c.reason}</span>}
                    </div>
                    {c.message && <div className={styles.conditionMessage}>{c.message}</div>}
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      </div>
    </>
  )
}
