import { useState } from 'react'
import { Outlet, useParams } from 'react-router'
import { useOrders } from '../../hooks/useOrders'
import { useMenus } from '../../hooks/useMenus'
import { usePreparations } from '../../hooks/usePreparations'
import PageHeader from '../../components/layout/PageHeader'
import ResourceNotFound from '../../components/layout/ResourceNotFound'
import Badge from '../../components/shared/Badge'
import { Button, PromoteIcon, RollbackIcon, TabNav } from '../../components/ui'
import PromoteDialog from '../../components/preparation/PromoteDialog'
import { paths } from '../../routes/paths'
import { promoteLabel } from '../../utils/preparations'
import type { PreparationContext } from './preparationContext'
import layout from '../../components/layout/layout.module.css'

export default function PreparationLayout() {
  const { namespace = '', name = '' } = useParams()
  const preparations = usePreparations()
  const orders = useOrders()
  const menus = useMenus()
  const [promoting, setPromoting] = useState(false)

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
  const menu = order?.menuRef ? menus?.find((m) => m.namespace === order.namespace && m.name === order.menuRef?.name) : undefined
  const editsAllowed = !!order && (order.menuRef ? !!menu && menu.overrides.patches.policy !== 'None' : true)

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
