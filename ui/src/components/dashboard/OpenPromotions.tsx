import { useState } from 'react'
import { Link } from 'react-router'
import type { Order, Preparation } from '../../api/types'
import { useOrders } from '../../hooks/useOrders'
import { usePreparations } from '../../hooks/usePreparations'
import { shortDigest } from '../../utils/format'
import { approvalLabel } from '../../utils/preparations'
import { paths } from '../../routes/paths'
import PromoteDialog from '../preparation/PromoteDialog'
import DataTable from '../layout/DataTable'
import EmptyState from '../layout/EmptyState'
import NameCell from '../layout/NameCell'
import RelativeTime from '../shared/RelativeTime'
import Btn from '../shared/Btn'
import layout from '../layout/layout.module.css'

interface Row {
  order: Order
  latest: Preparation
  active?: Preparation
}

/** Orders whose latest Preparation is not live yet, newest first. */
export default function OpenPromotions() {
  const orders = useOrders()
  const preparations = usePreparations()
  const [promoting, setPromoting] = useState<Preparation | null>(null)

  if (orders === null || preparations === null) return <EmptyState text="Loading…" />

  const prepByKey = new Map(preparations.map((p) => [`${p.namespace}/${p.name}`, p]))
  const rows: Row[] = orders
    .filter((o) => o.state === 'Ready' && !!o.latestRevision && o.latestRevision !== o.activePreparation)
    .flatMap((order) => {
      const latest = prepByKey.get(`${order.namespace}/${order.latestRevision}`)
      if (!latest) return []
      const active = order.activePreparation ? prepByKey.get(`${order.namespace}/${order.activePreparation}`) : undefined
      return [{ order, latest, active }]
    })
    .sort((a, b) => (b.latest.createdAt ?? '').localeCompare(a.latest.createdAt ?? ''))

  if (rows.length === 0) return <EmptyState text="All Orders are up to date." />

  const changesPath = (r: Row) =>
    r.active
      ? `${paths.preparation(r.latest.namespace, r.latest.name, 'changes')}?base=active`
      : paths.preparation(r.latest.namespace, r.latest.name)
  const message = (r: Row) => r.latest.commitMessage?.trim() || r.latest.name

  return (
    <>
      <DataTable<Row>
        rows={rows}
        rowKey={(r) => `${r.order.namespace}/${r.order.name}`}
        rowHref={changesPath}
        columns={[
          {
            header: 'Order',
            width: '24%',
            cell: (r) => <NameCell to={paths.order(r.order.namespace, r.order.name)} name={r.order.name} secondary={r.order.namespace} />,
          },
          {
            header: 'Change',
            truncate: true,
            title: message,
            cell: (r) => <Link className={layout.refLink} to={changesPath(r)}>{message(r)}</Link>,
          },
          {
            header: 'Digest',
            width: '190px',
            cell: (r) => (
              <span className={layout.mono}>
                {r.active ? shortDigest(r.active.artifact.digest) : 'new'} → {shortDigest(r.latest.artifact.digest)}
              </span>
            ),
          },
          {
            header: 'Approval',
            width: '150px',
            cell: (r) =>
              r.latest.approval
                ? approvalLabel(r.latest.approval.state, r.latest.approval.approvedCount, r.latest.approval.requiredApprovals)
                : <span className={layout.muted}>—</span>,
          },
          { header: 'Created', width: '110px', cell: (r) => <RelativeTime iso={r.latest.createdAt} /> },
          {
            header: '',
            width: '110px',
            cell: (r) => {
              if (r.order.mode !== 'Manual') return <span className={layout.muted}>Automatic</span>
              const blocked = !!r.latest.approval && !r.latest.approval.approved
              return (
                <Btn
                  variant="promote"
                  size="sm"
                  onClick={() => setPromoting(r.latest)}
                  disabled={blocked}
                  title={blocked ? r.latest.approval?.message ?? 'Approval required' : undefined}
                >
                  Promote
                </Btn>
              )
            },
          },
        ]}
      />

      {promoting && <PromoteDialog preparation={promoting} action="Promote" onClose={() => setPromoting(null)} />}
    </>
  )
}
