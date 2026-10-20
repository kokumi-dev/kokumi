import { useCallback, useState } from 'react'
import { promote } from '../../api/client'
import type { Preparation } from '../../api/types'
import { useOrders } from '../../hooks/useOrders'
import { useServings } from '../../hooks/useServings'
import ConfirmDialog from '../shared/ConfirmDialog'
import VulnerabilityGate from '../vulnerability/VulnerabilityGate'
import PromotionGates from './PromotionGates'
import { shortDigest } from '../../utils/format'
import { upstreamVerdict } from '../../utils/promotion'

interface Props {
  preparation: Preparation
  action: 'Promote' | 'Rollback'
  onClose: () => void
}

/**
 * PromoteDialog promotes a Preparation of a Manual Order. A promotion is an
 * intention: while a gate holds the Preparation, promoting queues it, and
 * the Serving deploys it once its gates pass.
 */
export default function PromoteDialog({ preparation: prep, action, onClose }: Props) {
  const orders = useOrders()
  const servings = useServings()
  const [checksHold, setChecksHold] = useState(false)
  const onChecksVerdict = useCallback((passes: boolean) => setChecksHold(!passes), [])
  const order = orders?.find((o) => o.namespace === prep.namespace && o.name === prep.order)
  const upstream = upstreamVerdict(prep, order, orders, servings)
  const holds = (!!prep.approval && !prep.approval.approved) || (!!upstream && !upstream.passes) || checksHold

  return (
    <ConfirmDialog
      title={`${action} ${prep.name}`}
      confirmLabel={holds ? (action === 'Rollback' ? 'Queue rollback' : 'Queue promotion') : action}
      variant={action === 'Rollback' ? 'rollback' : 'promote'}
      onCancel={onClose}
      onConfirm={async () => {
        await promote(prep.namespace, prep.order, prep.name)
        onClose()
      }}
    >
      {holds ? (
        <>
          The Serving of Order <strong>{prep.order}</strong> will switch to Preparation <strong>{prep.name}</strong>{' '}
          (digest <code>{shortDigest(prep.artifact.digest)}</code>) once its gates pass. Until then it is queued, and the
          Serving keeps what it deploys.
        </>
      ) : (
        <>
          The Serving of Order <strong>{prep.order}</strong> will switch to Preparation <strong>{prep.name}</strong>{' '}
          (digest <code>{shortDigest(prep.artifact.digest)}</code>).
        </>
      )}
      <PromotionGates preparation={prep} upstream={upstream} />
      {(prep.checks?.vulnerabilities || prep.images?.some((i) => i.vendored)) && (
        <VulnerabilityGate preparation={prep} onVerdict={onChecksVerdict} />
      )}
    </ConfirmDialog>
  )
}
