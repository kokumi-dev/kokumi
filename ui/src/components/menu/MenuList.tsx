import type { Menu } from '../../api/types'
import Badge from '../shared/Badge'
import styles from './MenuList.module.css'

interface Props {
  menus: Menu[]
  selectedName?: string
  query: string
  onSelect: (menu: Menu) => void
  onOrder?: (menu: Menu) => void
}

export default function MenuList({ menus, selectedName, query, onSelect, onOrder }: Props) {
  const filtered = query
    ? menus.filter((m) => m.name.toLowerCase().includes(query.toLowerCase()))
    : menus

  return (
    <>
      {filtered.length === 0 ? (
        <div className={styles.empty}>
          <svg width="40" height="40" viewBox="0 0 40 40" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round">
            <rect x="6" y="6" width="28" height="28" rx="4" />
            <path d="M14 14h12M14 20h12M14 26h8" />
          </svg>
          <span className={styles.emptyText}>
            {query ? 'No menus match your filter' : 'No menus found'}
          </span>
        </div>
      ) : (
        <div className={styles.grid}>
          {filtered.map((m) => (
            <MenuCard
              key={m.name}
              menu={m}
              selected={m.name === selectedName}
              onClick={() => onSelect(m)}
              onOrder={onOrder ? () => onOrder(m) : undefined}
            />
          ))}
        </div>
      )}
    </>
  )
}

interface CardProps {
  menu: Menu
  selected: boolean
  onClick: () => void
  onOrder?: () => void
}

function MenuCard({ menu: m, selected, onClick, onOrder }: CardProps) {
  return (
    <div
      className={`${styles.card} ${selected ? styles.cardSelected : ''}`}
      onClick={onClick}
      role="button"
      tabIndex={0}
      onKeyDown={(e) => e.key === 'Enter' && onClick()}
      aria-pressed={selected}
    >
      <div className={styles.cardHeader}>
        <div>
          <div className={styles.cardName}>{m.name}</div>
          <div className={styles.cardNs}>cluster-scoped</div>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
          {onOrder && (
            <button
              className={styles.orderBtn}
              onClick={(e) => { e.stopPropagation(); onOrder() }}
              title="Order from this Menu"
            >
              Order
            </button>
          )}
          <Badge state={m.state ?? ''} />
        </div>
      </div>

      <div className={styles.cardMeta}>
        <div className={styles.metaRow}>
          <span className={styles.metaLabel}>Source</span>
          <span className={styles.metaValue} title={m.source.oci}>{m.source.oci}</span>
        </div>
        <div className={styles.metaRow}>
          <span className={styles.metaLabel}>Version</span>
          <span className={styles.metaValue}>{m.source.version}</span>
        </div>
        <div className={styles.metaRow}>
          <span className={styles.metaLabel}>Values</span>
          <span className={styles.metaValue}>{m.overrides.values.policy}</span>
        </div>
        <div className={styles.metaRow}>
          <span className={styles.metaLabel}>Patches</span>
          <span className={styles.metaValue}>{m.overrides.patches.policy}</span>
        </div>
      </div>

      {m.defaults.autoDeploy === 'Enabled' && <span className={styles.autoDeployPill}>AUTO DEPLOY</span>}
    </div>
  )
}
