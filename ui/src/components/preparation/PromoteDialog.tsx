import { promote } from '../../api/client'
import type { Preparation } from '../../api/types'
import ConfirmDialog from '../shared/ConfirmDialog'
import { shortDigest } from '../../utils/format'

interface Props {
  preparation: Preparation
  action: 'Promote' | 'Rollback'
  onClose: () => void
}

export default function PromoteDialog({ preparation: prep, action, onClose }: Props) {
  return (
    <ConfirmDialog
      title={`${action} ${prep.name}`}
      confirmLabel={action}
      variant={action === 'Rollback' ? 'rollback' : 'promote'}
      onCancel={onClose}
      onConfirm={async () => {
        await promote(prep.namespace, prep.order, prep.name)
        onClose()
      }}
    >
      The Serving of Order <strong>{prep.order}</strong> will switch to Preparation{' '}
      <strong>{prep.name}</strong> (digest <code>{shortDigest(prep.artifact.digest)}</code>).
    </ConfirmDialog>
  )
}
