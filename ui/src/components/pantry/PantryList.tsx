import type { Pantry } from '../../api/types'
import Badge from '../shared/Badge'
import styles from './PantryList.module.css'

interface Props {
  pantries: Pantry[]
  selectedName?: string
  query: string
  onSelect: (pantry: Pantry) => void
}

export default function PantryList({ pantries, selectedName, query, onSelect }: Props) {
  const filtered = query
    ? pantries.filter((p) => p.name.toLowerCase().includes(query.toLowerCase()))
    : pantries

  return (
    <>
      {filtered.length === 0 ? (
        <div className={styles.empty}>
          <svg width="40" height="40" viewBox="0 0 40 40" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round">
            <rect x="6" y="6" width="28" height="28" rx="4" />
            <path d="M14 14h12M14 20h8M14 26h6" />
            <circle cx="30" cy="30" r="6" />
            <path d="M28 30h4M30 28v4" />
          </svg>
          <span className={styles.emptyText}>
            {query ? 'No pantries match your filter' : 'No pantries found'}
          </span>
        </div>
      ) : (
        <div className={styles.grid}>
          {filtered.map((p) => (
            <PantryCard
              key={p.name}
              pantry={p}
              selected={p.name === selectedName}
              onClick={() => onSelect(p)}
            />
          ))}
        </div>
      )}
    </>
  )
}

interface CardProps {
  pantry: Pantry
  selected: boolean
  onClick: () => void
}

function PantryCard({ pantry: p, selected, onClick }: CardProps) {
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
          <div className={styles.cardName}>{p.name}</div>
          <div className={styles.cardNs}>{p.namespace}</div>
        </div>
        <Badge state={p.state ?? ''} />
      </div>

      <div className={styles.cardMeta}>
        <div className={styles.metaRow}>
          <span className={styles.metaLabel}>Registry</span>
          <span className={styles.metaValue} title={p.registry}>{p.registry}</span>
        </div>
        {p.secretRef && (
          <div className={styles.metaRow}>
            <span className={styles.metaLabel}>Secret</span>
            <span className={styles.metaValue}>{p.secretRef}</span>
          </div>
        )}
      </div>

      {p.description && (
        <div className={styles.description}>{p.description}</div>
      )}
    </div>
  )
}
