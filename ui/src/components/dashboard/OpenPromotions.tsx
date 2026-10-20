import { Fragment, useState } from 'react'
import { Link } from 'react-router'
import type { Order, Preparation } from '../../api/types'
import { useOrders } from '../../hooks/useOrders'
import { usePreparations } from '../../hooks/usePreparations'
import { useServings } from '../../hooks/useServings'
import { useCan, useDeniedReason } from '../../hooks/useCapability'
import { useAppInfo } from '../../appContext'
import { shortDigest } from '../../utils/format'
import { approvalNeed, humanSteps, viewerMayApprove, type HumanSteps } from '../../utils/promotion'
import { paths } from '../../routes/paths'
import PromoteDialog from '../preparation/PromoteDialog'
import DataTable from '../layout/DataTable'
import EmptyState from '../layout/EmptyState'
import NameCell from '../layout/NameCell'
import RelativeTime from '../shared/RelativeTime'
import { Button, ButtonLink, EyeIcon, PromoteIcon } from '../ui'
import layout from '../layout/layout.module.css'

interface Row {
  order: Order
  latest: Preparation
  active?: Preparation
  /** What a person still has to do before the latest Preparation deploys. */
  steps: HumanSteps
}

/**
 * Orders whose latest Preparation is not live yet and still needs a person,
 * newest first: a promote under Manual, an approval under an approval
 * policy. A Preparation that only waits for the Serving's own gates, such as
 * a soak, is not open. Each row offers the viewer's next step.
 */
export default function OpenPromotions() {
  const orders = useOrders()
  const preparations = usePreparations()
  const servings = useServings()
  const { viewer } = useAppInfo()
  const [promoting, setPromoting] = useState<Preparation | null>(null)
  const promoteDenied = useDeniedReason('preparations:promote')
  const canApprove = useCan('preparations:approve')

  if (orders === null || preparations === null) return <EmptyState text="Loading…" />

  const prepByKey = new Map(preparations.map((p) => [`${p.namespace}/${p.name}`, p]))
  let waiting = 0
  const rows: Row[] = orders
    .filter((o) => o.state === 'Ready' && !!o.latestRevision && o.latestRevision !== o.activePreparation)
    .flatMap((order) => {
      const latest = prepByKey.get(`${order.namespace}/${order.latestRevision}`)
      if (!latest) return []
      const steps = humanSteps(order, latest, servings)
      if (!steps) {
        waiting++
        return []
      }
      const active = order.activePreparation ? prepByKey.get(`${order.namespace}/${order.activePreparation}`) : undefined
      return [{ order, latest, active, steps }]
    })
    .sort((a, b) => (b.latest.createdAt ?? '').localeCompare(a.latest.createdAt ?? ''))

  if (rows.length === 0) {
    return (
      <EmptyState
        text={
          waiting
            ? 'Nothing waits for a person: the latest Preparations are queued or wait for their soaks and checks.'
            : 'All Orders are up to date.'
        }
      />
    )
  }

  const changesPath = (r: Row) =>
    r.active
      ? `${paths.preparation(r.latest.namespace, r.latest.name, 'changes')}?base=active`
      : paths.preparation(r.latest.namespace, r.latest.name)
  const message = (r: Row) => r.latest.commitMessage?.trim() || r.latest.name

  /**
   * The viewer's next step: review when they may approve and have not, else
   * promote, else nothing: Waiting for says who has to act. Who may do both
   * reviews first, so they look before they ship. Who may only promote
   * queues the promotion, which deploys once the approvals arrive.
   */
  function nextStep(r: Row) {
    if (r.steps.approval && canApprove && viewerMayApprove(r.latest, viewer)) {
      return (
        <ButtonLink size="sm" block icon={<EyeIcon />} to={paths.preparation(r.latest.namespace, r.latest.name, 'approvals')}>
          Review
        </ButtonLink>
      )
    }
    if (r.steps.promote) {
      return (
        <Button variant="promote" size="sm" block icon={<PromoteIcon />} onClick={() => setPromoting(r.latest)} disabledReason={promoteDenied}>
          Promote
        </Button>
      )
    }
    return null
  }

  /**
   * What the row waits for, e.g. "1 approval from release-approvers, then promote",
   * with the viewer's own approval on a line below when they gave it. Group
   * names never break, so the cell wraps before them.
   */
  function waitingFor(r: Row) {
    const { promote, approval } = r.steps
    const approved = r.latest.approval?.votes?.some((v) => v.subject === viewer?.subject && v.decision === 'Approve')
    return (
      <>
        {approval && (
          <>
            {approvalNeed(approval)} from{' '}
            {approval.groups.map((g, i) => (
              <Fragment key={g}>
                {i > 0 && ' or '}
                <span className={layout.nobreak}>{g}</span>
              </Fragment>
            ))}
          </>
        )}
        {approval && promote && ', then '}
        {promote && 'promote'}
        {approved && <div className={layout.muted}>You approved</div>}
      </>
    )
  }

  return (
    <>
      <DataTable<Row>
        rows={rows}
        rowKey={(r) => `${r.order.namespace}/${r.order.name}`}
        rowHref={changesPath}
        columns={[
          {
            header: 'Order',
            width: '22%',
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
            width: '180px',
            cell: (r) => (
              <span className={layout.mono}>
                {r.active ? shortDigest(r.active.artifact.digest) : 'new'} → {shortDigest(r.latest.artifact.digest)}
              </span>
            ),
          },
          { header: 'Waiting for', width: '240px', wrap: true, cell: waitingFor },
          { header: 'Created', width: '110px', cell: (r) => <RelativeTime iso={r.latest.createdAt} /> },
          { header: 'Action', width: '130px', cell: nextStep },
        ]}
      />

      {promoting && <PromoteDialog preparation={promoting} action="Promote" onClose={() => setPromoting(null)} />}
    </>
  )
}
