import type { Patch } from '../../api/types'
import styles from './PatchList.module.css'

interface Props {
  patches: Patch[]
  /** When set, each patch and path gets a remove button. */
  onRemove?: (index: number) => void
  onRemovePath?: (index: number, path: string) => void
}

function RemoveIcon({ size }: { size: number }) {
  return (
    <svg viewBox="0 0 14 14" width={size} height={size} fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
      <path d="M2 2l10 10M12 2L2 12" />
    </svg>
  )
}

export default function PatchList({ patches, onRemove, onRemovePath }: Props) {
  return (
    <div className={styles.list}>
      {patches.map((p, i) => {
        const target = `${p.target.kind}/${p.target.name}${p.target.namespace ? ` (${p.target.namespace})` : ''}`
        return (
          <div key={i} className={styles.item}>
            <div className={styles.itemHeader}>
              <span className={styles.target}>{target}</span>
              {onRemove && (
                <button
                  className={styles.removeBtn}
                  title="Remove all changes for this target"
                  aria-label={`Remove changes for ${target}`}
                  onClick={() => onRemove(i)}
                >
                  <RemoveIcon size={12} />
                </button>
              )}
            </div>
            {Object.entries(p.set).map(([path, v]) => (
              <div key={path} className={styles.setRow}>
                <span className={styles.setKey}>{path}</span>
                <span>→</span>
                <span>{v}</span>
                {onRemovePath && (
                  <button
                    className={styles.removeBtn}
                    title={`Remove ${path}`}
                    aria-label={`Remove ${path}`}
                    onClick={() => onRemovePath(i, path)}
                  >
                    <RemoveIcon size={10} />
                  </button>
                )}
              </div>
            ))}
          </div>
        )
      })}
    </div>
  )
}
