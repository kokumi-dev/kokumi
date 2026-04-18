import type { Menu } from '../../api/types'
import Badge from '../shared/Badge'
import Btn from '../shared/Btn'
import styles from './MenuDetail.module.css'

interface Props {
  menu: Menu
  onClose: () => void
  onEdit: (menu: Menu) => void
  onDelete: (menu: Menu) => void
  onOrder?: (menu: Menu) => void
}

export default function MenuDetail({ menu, onClose, onEdit, onDelete, onOrder }: Props) {
  return (
    <>
      <div className={styles.backdrop} onClick={onClose} />

      <div className={styles.panel}>
        {/* Header */}
        <div className={styles.header}>
          <div className={styles.headerLeft}>
            <span className={styles.title}>{menu.name}</span>
            <span className={styles.subtitle}>cluster-scoped</span>
          </div>
          <div className={styles.headerActions}>
            <Badge phase={menu.phase ?? ''} />
            {onOrder && (
              <Btn variant="primary" size="sm" onClick={() => onOrder(menu)}>
                Order
              </Btn>
            )}
            <Btn variant="secondary" size="sm" onClick={() => onEdit(menu)}>
              Edit
            </Btn>
            <Btn variant="danger" size="sm" onClick={() => onDelete(menu)}>
              Delete
            </Btn>
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
              <span className={styles.specKey}>Source OCI</span>
              <span className={styles.specValue}>{menu.source.oci}</span>
              <span className={styles.specKey}>Version</span>
              <span className={styles.specValue}>{menu.source.version}</span>
              <span className={styles.specKey}>Auto Deploy Default</span>
              <span className={styles.specValue}>{menu.defaults.autoDeploy ? 'Yes' : 'No'}</span>
              {menu.render?.helm && (
                <>
                  <span className={styles.specKey}>Renderer</span>
                  <span className={styles.specValue}>Helm</span>
                </>
              )}
            </div>
          </div>

          {/* Override Policies */}
          <div className={styles.section}>
            <span className={styles.sectionTitle}>Override Policies</span>
            <div className={styles.specGrid}>
              <span className={styles.specKey}>Values Policy</span>
              <span className={styles.specValue}>{menu.overrides.values.policy}</span>
              {menu.overrides.values.policy === 'Restricted' && menu.overrides.values.allowed && (
                <>
                  <span className={styles.specKey}>Allowed Values</span>
                  <span className={styles.specValue}>{menu.overrides.values.allowed.join(', ')}</span>
                </>
              )}
              <span className={styles.specKey}>Patches Policy</span>
              <span className={styles.specValue}>{menu.overrides.patches.policy}</span>
              {menu.overrides.patches.policy === 'Restricted' && menu.overrides.patches.allowed && (
                <>
                  <span className={styles.specKey}>Allowed Patches</span>
                  <span className={styles.specValue}>
                    {menu.overrides.patches.allowed.map((a) =>
                      `${a.target.kind}/${a.target.name}: ${a.paths.join(', ')}`,
                    ).join('; ')}
                  </span>
                </>
              )}
            </div>
          </div>

          {/* Base Patches */}
          {menu.patches && menu.patches.length > 0 && (
            <div className={styles.section}>
              <span className={styles.sectionTitle}>Base Patches</span>
              {menu.patches.map((p, i) => (
                <div key={i} className={styles.specGrid}>
                  <span className={styles.specKey}>Target</span>
                  <span className={styles.specValue}>
                    {p.target.kind}/{p.target.name}
                    {p.target.namespace ? ` (${p.target.namespace})` : ''}
                  </span>
                  {Object.entries(p.set).map(([k, v]) => (
                    <span key={k} className={styles.specValue} style={{ gridColumn: '1 / -1' }}>
                      {k}: {v}
                    </span>
                  ))}
                </div>
              ))}
            </div>
          )}

          {/* Conditions */}
          {menu.conditions && menu.conditions.length > 0 && (
            <div className={styles.section}>
              <span className={styles.sectionTitle}>Conditions</span>
              {menu.conditions.map((c) => (
                <div key={c.type} className={styles.specGrid}>
                  <span className={styles.specKey}>{c.type}</span>
                  <span className={styles.specValue}>
                    {c.status} — {c.message}
                  </span>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </>
  )
}
