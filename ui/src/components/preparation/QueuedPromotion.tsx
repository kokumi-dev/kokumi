import { useState } from 'react'
import { promote } from '../../api/client'
import type { Preparation } from '../../api/types'
import { useDeniedReason } from '../../hooks/useCapability'
import type { QueuedPromotion as Queued } from '../../utils/promotion'
import Badge from '../shared/Badge'
import ConfirmDialog from '../shared/ConfirmDialog'
import { Button, CloseIcon, IconButton } from '../ui'
import styles from './QueuedPromotion.module.css'

interface Props {
  preparation: Preparation
  queued: Queued
  /** Fits a list row: what it waits for moves into the tooltip, and Withdraw into an icon. */
  compact?: boolean
}

/**
 * QueuedPromotion shows a Preparation that is promoted and waits for its
 * gates: the next one its Serving deploys. Withdrawing it promotes the
 * deployed Preparation again, so the Serving keeps it.
 */
export default function QueuedPromotion({ preparation: prep, queued, compact }: Props) {
  const [withdrawing, setWithdrawing] = useState(false)
  const promoteDenied = useDeniedReason('preparations:promote')
  const deployed = queued.serving.deployedPreparation
  const waits = `Next, waits for ${queued.waitingFor}`

  return (
    <span className={styles.queued}>
      <Badge state="Queued" tone="warning" title={compact ? `${waits}: ${queued.message}` : queued.message} />
      {!compact && <span className={styles.waiting} title={queued.message}>{waits}</span>}
      {deployed && !compact && (
        <Button variant="ghost" size="sm" onClick={() => setWithdrawing(true)} disabledReason={promoteDenied}>
          Withdraw
        </Button>
      )}
      {deployed && compact && (
        <IconButton
          aria-label={promoteDenied ?? `Withdraw the promotion of ${prep.name}`}
          tone="danger"
          onClick={() => setWithdrawing(true)}
          disabled={!!promoteDenied}
        >
          <CloseIcon />
        </IconButton>
      )}
      {withdrawing && deployed && (
        <ConfirmDialog
          title={`Withdraw the promotion of ${prep.name}`}
          confirmLabel="Withdraw"
          onCancel={() => setWithdrawing(false)}
          onConfirm={async () => {
            await promote(prep.namespace, prep.order, deployed)
            setWithdrawing(false)
          }}
        >
          The Serving of Order <strong>{prep.order}</strong> keeps deploying <strong>{deployed}</strong> and does not
          deploy <strong>{prep.name}</strong> when its gates pass.
        </ConfirmDialog>
      )}
    </span>
  )
}
