import type { Order } from '../../api/types'
import Badge from '../shared/Badge'
import styles from './OrderList.module.css'

interface Props {
  orders: Order[]
  selectedName?: string
  query: string
  onSelect: (order: Order) => void
}

/**
 * OrderList renders a filterable card grid of all Orders. Clicking a card
 * fires onSelect; the "Add Order" button fires onAdd.
 */
export default function OrderList({ orders, selectedName, query, onSelect }: Props) {
  const filtered = query
    ? orders.filter((r) =>
        r.name.toLowerCase().includes(query.toLowerCase()) ||
        r.namespace.toLowerCase().includes(query.toLowerCase()),
      )
    : orders

  return (
    <>
      {filtered.length === 0 ? (
        <div className={styles.empty}>
          <svg width="40" height="40" viewBox="0 0 40 40" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round">
            <path d="M10 4v32M10 14h14a6 6 0 0 1 0 12H10" />
          </svg>
          <span className={styles.emptyText}>
            {query ? 'No orders match your filter' : 'No orders found'}
          </span>
        </div>
      ) : (
        <div className={styles.grid}>
          {filtered.map((r) => (
            <OrderCard
              key={`${r.namespace}/${r.name}`}
              order={r}
              selected={r.name === selectedName && r.namespace === r.namespace}
              onClick={() => onSelect(r)}
            />
          ))}
        </div>
      )}
    </>
  )
}

// ── OrderCard ────────────────────────────────────────────────────────────────

interface CardProps {
  order: Order
  selected: boolean
  onClick: () => void
}

function OrderCard({ order: r, selected, onClick }: CardProps) {
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
          <div className={styles.cardName}>{r.name}</div>
          <div className={styles.cardNs}>{r.namespace}</div>
        </div>
        <Badge phase={r.phase} />
      </div>

      <div className={styles.cardMeta}>
        <div className={styles.metaRow}>
          <span className={styles.metaLabel}>Source</span>
          <span className={styles.metaValue} title={r.source.oci}>{r.source.oci}</span>
        </div>
        <div className={styles.metaRow}>
          <span className={styles.metaLabel}>Version</span>
          <span className={styles.metaValue}>{r.source.version}</span>
        </div>
        {r.latestRevision && (
          <div className={styles.metaRow}>
            <span className={styles.metaLabel}>Latest</span>
            <span className={styles.metaValue}>{r.latestRevision}</span>
          </div>
        )}
        {r.activePreparation && (
          <div className={styles.metaRow}>
            <span className={styles.metaLabel}>Active</span>
            <span className={styles.metaValue}>{r.activePreparation}</span>
          </div>
        )}
      </div>

      {r.autoDeploy && <span className={styles.autoDeployPill}>AUTO DEPLOY</span>}
    </div>
  )
}
