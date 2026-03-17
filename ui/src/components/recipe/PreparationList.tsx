import { useState } from 'react'
import type { Preparation } from '../../api/types'
import Badge from '../shared/Badge'
import Btn from '../shared/Btn'
import styles from './PreparationList.module.css'

interface Props {
  preparations: Preparation[]
  /** Called when a promote/rollback action is confirmed. */
  onPromote: (prep: Preparation) => Promise<void>
  /** Opens the manifest view for the given Preparation. */
  onManifest: (prep: Preparation) => void
  /** Opens the diff view comparing prep to the current active. */
  onDiff: (prep: Preparation) => void
}

/**
 * PreparationList renders all Preparations for a Order, sorted newest-first.
 * Each row shows phase, metadata, and context-aware action buttons:
 *   - Promote / Rollback  (hidden when this IS the active Preparation)
 *   - Manifest            (always visible)
 *   - Diff                (visible when there is an active Preparation to diff against)
 */
export default function PreparationList({
  preparations,
  onPromote,
  onManifest,
  onDiff,
}: Props) {
  const activePrepCreatedAt = preparations.find((p) => p.isActive)?.createdAt

  if (preparations.length === 0) {
    return <p className={styles.empty}>No preparations found for this order.</p>
  }

  return (
    <div className={styles.list}>
      {preparations.map((prep) => (
        <PreparationRow
          key={`${prep.namespace}/${prep.name}`}
          prep={prep}
          activePrepCreatedAt={activePrepCreatedAt}
          hasActive={preparations.some((p) => p.isActive)}
          onPromote={onPromote}
          onManifest={onManifest}
          onDiff={onDiff}
        />
      ))}
    </div>
  )
}

// ── PreparationRow ────────────────────────────────────────────────────────────

interface RowProps {
  prep: Preparation
  activePrepCreatedAt?: string
  hasActive: boolean
  onPromote: (prep: Preparation) => Promise<void>
  onManifest: (prep: Preparation) => void
  onDiff: (prep: Preparation) => void
}

function PreparationRow({
  prep,
  activePrepCreatedAt,
  hasActive,
  onPromote,
  onManifest,
  onDiff,
}: RowProps) {
  const [promoting, setPromoting] = useState(false)

  async function handlePromote() {
    setPromoting(true)
    try {
      await onPromote(prep)
    } finally {
      setPromoting(false)
    }
  }

  const promoteLabel = resolvePromoteLabel(prep.createdAt, activePrepCreatedAt)
  const canDiff = hasActive && !prep.isActive

  return (
    <div className={`${styles.row} ${prep.isActive ? styles.rowActive : ''}`}>
      <div className={styles.info}>
        <div className={styles.nameRow}>
          <span className={styles.name}>{prep.name}</span>
          {prep.isActive && <span className={styles.activePill}>ACTIVE</span>}
          <Badge phase={prep.phase} />
        </div>

        <div className={styles.meta}>
          {prep.createdAt && (
            <span className={styles.metaItem}>
              {new Date(prep.createdAt).toLocaleString()}
            </span>
          )}
          <span className={styles.metaItem}>
            configHash{' '}
            <span className={styles.metaItemValue}>
              {prep.configHash.replace('sha256:', '').slice(0, 12)}…
            </span>
          </span>
          <span className={styles.metaItem}>
            digest{' '}
            <span className={styles.metaItemValue}>
              {prep.artifact.digest.replace('sha256:', '').slice(0, 12)}…
            </span>
          </span>
        </div>
      </div>

      <div className={styles.actions}>
        {!prep.isActive && (
          <Btn
            variant={promoteLabel === 'Rollback' ? 'rollback' : 'promote'}
            size="sm"
            onClick={handlePromote}
            disabled={promoting}
          >
            {promoting ? '…' : promoteLabel}
          </Btn>
        )}

        <Btn variant="ghost" size="sm" onClick={() => onManifest(prep)}>
          Manifest
        </Btn>

        {canDiff && (
          <Btn variant="ghost" size="sm" onClick={() => onDiff(prep)}>
            Diff
          </Btn>
        )}
      </div>
    </div>
  )
}

// ── Label resolution ──────────────────────────────────────────────────────────

/**
 * Returns "Rollback" when the preparation is older than the currently active
 * one, and "Promote" otherwise (including when there is no active or the dates
 * cannot be compared).
 */
function resolvePromoteLabel(
  prepCreatedAt?: string,
  activeCreatedAt?: string,
): 'Promote' | 'Rollback' {
  if (!prepCreatedAt || !activeCreatedAt) return 'Promote'
  return new Date(prepCreatedAt) < new Date(activeCreatedAt) ? 'Rollback' : 'Promote'
}
