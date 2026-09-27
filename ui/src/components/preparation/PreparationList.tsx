import { useState } from 'react'
import type { Preparation, PromotionMode } from '../../api/types'
import Badge from '../shared/Badge'
import Btn from '../shared/Btn'
import styles from './PreparationList.module.css'

interface Props {
  preparations: Preparation[]
  /** Promotion mode of the Order; Automatic Orders cannot be promoted manually. */
  mode: PromotionMode
  /** Called when a promote/rollback action is confirmed. */
  onPromote: (prep: Preparation) => Promise<void>
  /** Opens the manifest view for the given Preparation. */
  onManifest: (prep: Preparation) => void
  /** Opens the diff view comparing prep to the current active. */
  onDiff: (prep: Preparation) => void
  /** Opens the approval history and voting view. */
  onApprovals: (prep: Preparation) => void
}

/**
 * PreparationList renders all Preparations for an Order, sorted newest-first.
 * Each row shows state, metadata, and context-aware action buttons:
 *   - Promote / Rollback  (Manual Orders only; disabled until approved)
 *   - Approvals           (visible when the Preparation has an approval policy)
 *   - Manifest            (always visible)
 *   - Diff                (visible when there is an active Preparation to diff against)
 */
export default function PreparationList({
  preparations,
  mode,
  onPromote,
  onManifest,
  onDiff,
  onApprovals,
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
          mode={mode}
          onPromote={onPromote}
          onManifest={onManifest}
          onDiff={onDiff}
          onApprovals={onApprovals}
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
  mode: PromotionMode
  onPromote: (prep: Preparation) => Promise<void>
  onManifest: (prep: Preparation) => void
  onDiff: (prep: Preparation) => void
  onApprovals: (prep: Preparation) => void
}

function PreparationRow({
  prep,
  activePrepCreatedAt,
  hasActive,
  mode,
  onPromote,
  onManifest,
  onDiff,
  onApprovals,
}: RowProps) {
  const [promoting, setPromoting] = useState(false)
  const [promoteError, setPromoteError] = useState<string | null>(null)

  async function handlePromote() {
    setPromoting(true)
    setPromoteError(null)
    try {
      await onPromote(prep)
    } catch (e) {
      setPromoteError((e as Error).message)
    } finally {
      setPromoting(false)
    }
  }

  const promoteLabel = resolvePromoteLabel(prep.createdAt, activePrepCreatedAt)
  const canDiff = hasActive && !prep.isActive
  const approval = prep.approval
  const blockedByApproval = !!approval && !approval.approved

  return (
    <div className={`${styles.row} ${prep.isActive ? styles.rowActive : ''}`}>
      <div className={styles.info}>
        <div className={styles.nameRow}>
          <span className={styles.name}>{prep.name}</span>
          {prep.isActive && <span className={styles.activePill}>ACTIVE</span>}
          <Badge state={prep.state} />
          {approval && (
            <span
              className={`${styles.approvalPill} ${approvalClass(approval.state)}`}
              title={approval.message}
            >
              {approval.sealedAt ? 'Sealed · ' : ''}
              {approvalLabel(approval.state, approval.approvedCount, approval.requiredApprovals)}
            </span>
          )}
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
          {prep.gitSource?.sourceLink && (
            <span className={styles.metaItem}>
              source{' '}
              <a
                className={styles.metaItemLink}
                href={prep.gitSource.sourceLink.url}
                target="_blank"
                rel="noreferrer noopener"
                title={prep.gitSource.tag || prep.gitSource.commitHash}
              >
                {prep.gitSource.sourceLink.label} ↗
              </a>
            </span>
          )}
        </div>
        {promoteError && <span className={styles.error}>{promoteError}</span>}
      </div>

      <div className={styles.actions}>
        {!prep.isActive && mode === 'Manual' && (
          <Btn
            variant={promoteLabel === 'Rollback' ? 'rollback' : 'promote'}
            size="sm"
            onClick={handlePromote}
            disabled={promoting || blockedByApproval}
            title={blockedByApproval ? approval?.message ?? 'Approval required' : undefined}
          >
            {promoting ? '…' : promoteLabel}
          </Btn>
        )}

        {approval && (
          <Btn variant="ghost" size="sm" onClick={() => onApprovals(prep)}>
            Approvals
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

function approvalLabel(state: string, approved: number, required: number): string {
  switch (state) {
    case 'Approved':
      return 'Approved'
    case 'ChangesRequested':
      return 'Changes requested'
    default:
      return `${approved}/${required} approvals`
  }
}

function approvalClass(state: string): string {
  switch (state) {
    case 'Approved':
      return styles.approvalApproved
    case 'ChangesRequested':
      return styles.approvalRejected
    default:
      return styles.approvalPending
  }
}
