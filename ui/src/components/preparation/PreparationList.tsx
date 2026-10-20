import { useState } from 'react'
import { Link } from 'react-router'
import type { Preparation, Product, PromotionMode } from '../../api/types'
import StatusIndicator from '../shared/StatusIndicator'
import RelativeTime from '../shared/RelativeTime'
import PreparationFindings from '../vulnerability/PreparationFindings'
import { Button, PromoteIcon, RollbackIcon } from '../ui'
import PromoteDialog from './PromoteDialog'
import QueuedPromotion from './QueuedPromotion'
import { paths } from '../../routes/paths'
import { shortDigest } from '../../utils/format'
import { approvalLabel, promoteLabel } from '../../utils/preparations'
import { useDeniedReason } from '../../hooks/useCapability'
import { useServings } from '../../hooks/useServings'
import { queuedPromotion } from '../../utils/promotion'
import layout from '../layout/layout.module.css'
import styles from './PreparationList.module.css'

interface Props {
  /** Sorted newest-first. */
  preparations: Preparation[]
  /** Promotion mode of the Order; Automatic Orders cannot be promoted manually. */
  mode: PromotionMode
  /** The Order's Product, whose scans give each Preparation's findings. */
  product?: Product
}

/**
 * PreparationList renders the history of an Order like a commit log. Each row
 * links to the Preparation and offers promote/rollback for Manual Orders; a
 * promoted one that waits for its gates shows as queued.
 */
export default function PreparationList({ preparations, mode, product }: Props) {
  const [promoting, setPromoting] = useState<{ prep: Preparation; action: 'Promote' | 'Rollback' } | null>(null)
  const promoteDenied = useDeniedReason('preparations:promote')
  const servings = useServings()
  const active = preparations.find((p) => p.isActive)

  if (preparations.length === 0) {
    return <p className={styles.empty}>No preparations found for this order.</p>
  }

  return (
    <>
      <ul className={styles.list}>
        {preparations.map((prep) => {
          const action = promoteLabel(prep.createdAt, active?.createdAt)
          const approval = prep.approval
          const queued = mode === 'Manual' ? queuedPromotion(prep, servings) : undefined
          const title = prep.commitMessage?.trim() || prep.name
          return (
            <li key={`${prep.namespace}/${prep.name}`} className={styles.row}>
              <div className={styles.info}>
                <div className={layout.cellRow}>
                  <Link className={layout.primaryLink} to={paths.preparation(prep.namespace, prep.name)} title={title}>
                    {title}
                  </Link>
                  {prep.isActive && <span className={layout.label}>Active</span>}
                </div>
                <div className={styles.meta}>
                  <span>{prep.name}</span>
                  <RelativeTime iso={prep.createdAt} />
                  <span className={layout.mono} title={prep.artifact.digest}>{shortDigest(prep.artifact.digest)}</span>
                  {approval && (
                    <span title={approval.message}>
                      {approval.sealedAt ? 'Sealed, ' : ''}
                      {approvalLabel(approval.state, approval.approvedCount, approval.requiredApprovals)}
                    </span>
                  )}
                  {prep.gitSource?.sourceLink && (
                    <a
                      className={layout.refLink}
                      href={prep.gitSource.sourceLink.url}
                      target="_blank"
                      rel="noreferrer noopener"
                      title={prep.gitSource.tag || prep.gitSource.commitHash}
                    >
                      {prep.gitSource.sourceLink.label} ↗
                    </a>
                  )}
                </div>
              </div>

              <span className={`${styles.version} ${layout.mono}`} title={prep.version && `Rendered from version ${prep.version}`}>
                {prep.version}
              </span>

              <div className={styles.findings}>
                <PreparationFindings preparation={prep} product={product} />
              </div>

              <StatusIndicator state={prep.state} />

              <div className={styles.actions}>
                {queued ? (
                  <QueuedPromotion preparation={prep} queued={queued} compact />
                ) : !prep.isActive && mode === 'Manual' && (
                  <Button
                    variant={action === 'Rollback' ? 'rollback' : 'promote'}
                    size="sm"
                    icon={action === 'Rollback' ? <RollbackIcon /> : <PromoteIcon />}
                    onClick={() => setPromoting({ prep, action })}
                    disabledReason={promoteDenied}
                  >
                    {action}
                  </Button>
                )}
              </div>
            </li>
          )
        })}
      </ul>

      {promoting && (
        <PromoteDialog
          preparation={promoting.prep}
          action={promoting.action}
          onClose={() => setPromoting(null)}
        />
      )}
    </>
  )
}
