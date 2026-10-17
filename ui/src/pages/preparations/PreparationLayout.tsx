import { useState } from 'react'
import { Outlet, useParams } from 'react-router'
import { useOrders } from '../../hooks/useOrders'
import { useProducts } from '../../hooks/useProducts'
import { usePreparations } from '../../hooks/usePreparations'
import { useCan, useDeniedReason } from '../../hooks/useCapability'
import PageHeader from '../../components/layout/PageHeader'
import ResourceNotFound from '../../components/layout/ResourceNotFound'
import Badge from '../../components/shared/Badge'
import { Button, PromoteIcon, RollbackIcon, TabNav } from '../../components/ui'
import PromoteDialog from '../../components/preparation/PromoteDialog'
import { paths } from '../../routes/paths'
import { promoteLabel } from '../../utils/preparations'
import { allowsPatches, productOf } from '../../utils/products'
import type { PreparationContext } from './preparationContext'
import layout from '../../components/layout/layout.module.css'

export default function PreparationLayout() {
  const { namespace = '', name = '' } = useParams()
  const preparations = usePreparations()
  const orders = useOrders()
  const products = useProducts()
  const [promoting, setPromoting] = useState(false)
  const promoteDenied = useDeniedReason('preparations:promote')
  const canEditOrders = useCan('orders:update')

  const prep = preparations?.find((p) => p.namespace === namespace && p.name === name)
  if (!prep) {
    return (
      <ResourceNotFound
        kind="Preparation"
        listPath={paths.preparations()}
        listLabel="Preparations"
        namespace={namespace}
        name={name}
        loading={preparations === null}
      />
    )
  }

  const siblings = preparations!.filter((p) => p.namespace === prep.namespace && p.order === prep.order)
  const parent = prep.parentDigest ? siblings.find((p) => p.artifact.digest === prep.parentDigest) : undefined
  const active = siblings.find((p) => p.isActive)
  const order = orders?.find((o) => o.namespace === prep.namespace && o.name === prep.order)
  // Until the Product has loaded, deny edits (fail closed).
  const product = order && productOf(products, order)
  const editsAllowed = canEditOrders && !!product && allowsPatches(product)

  const action = promoteLabel(prep.createdAt, active?.createdAt)
  const blockedByApproval = !!prep.approval && !prep.approval.approved
  const canPromote = !prep.isActive && order?.mode === 'Manual'

  const ctx: PreparationContext = { prep, order, parent, active, editsAllowed }

  return (
    <div className={layout.page}>
      <PageHeader
        breadcrumbs={[
          { label: 'Preparations', to: paths.preparations() },
          { label: prep.namespace },
          { label: prep.order, to: paths.order(prep.namespace, prep.order, 'preparations') },
        ]}
        title={prep.name}
        badge={
          <>
            <Badge state={prep.state} />
            {prep.isActive && <span className={layout.label}>Active</span>}
          </>
        }
        subtitle={prep.commitMessage?.trim() || undefined}
        actions={canPromote && (
          <Button
            variant={action === 'Rollback' ? 'rollback' : 'promote'}
            icon={action === 'Rollback' ? <RollbackIcon /> : <PromoteIcon />}
            onClick={() => setPromoting(true)}
            disabled={blockedByApproval}
            disabledReason={promoteDenied}
            title={blockedByApproval ? prep.approval?.message ?? 'Approval required' : undefined}
          >
            {action}
          </Button>
        )}
      />

      <TabNav
        tabs={[
          { to: paths.preparation(prep.namespace, prep.name), label: 'Overview', end: true },
          { to: paths.preparation(prep.namespace, prep.name, 'files'), label: 'Files' },
          { to: paths.preparation(prep.namespace, prep.name, 'changes'), label: 'Changes' },
          { to: paths.preparation(prep.namespace, prep.name, 'images'), label: 'Images', count: prep.images?.length ?? 0 },
          { to: paths.preparation(prep.namespace, prep.name, 'intent'), label: 'Intent' },
          { to: paths.preparation(prep.namespace, prep.name, 'artifact'), label: 'Artifact' },
          ...(prep.approval
            ? [{ to: paths.preparation(prep.namespace, prep.name, 'approvals'), label: 'Approvals', count: prep.approval.submissionCount }]
            : []),
        ]}
      />

      <Outlet context={ctx} />

      {promoting && <PromoteDialog preparation={prep} action={action} onClose={() => setPromoting(false)} />}
    </div>
  )
}
