import type { Patch } from '../../api/types'
import { CloseIcon, IconButton } from '../ui'
import styles from './PatchList.module.css'

interface Props {
  patches: Patch[]
  /** When set, each patch and path gets a remove button. */
  onRemove?: (index: number) => void
  onRemovePath?: (index: number, path: string) => void
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
                <IconButton
                  size="xs"
                  tone="danger"
                  title="Remove all changes for this target"
                  aria-label={`Remove changes for ${target}`}
                  onClick={() => onRemove(i)}
                >
                  <CloseIcon size={12} />
                </IconButton>
              )}
            </div>
            {Object.entries(p.set).map(([path, v]) => (
              <div key={path} className={styles.setRow}>
                <span className={styles.setKey}>{path}</span>
                <span>→</span>
                <span>{v}</span>
                {onRemovePath && (
                  <IconButton
                    size="xs"
                    tone="danger"
                    aria-label={`Remove ${path}`}
                    onClick={() => onRemovePath(i, path)}
                  >
                    <CloseIcon size={12} />
                  </IconButton>
                )}
              </div>
            ))}
          </div>
        )
      })}
    </div>
  )
}
