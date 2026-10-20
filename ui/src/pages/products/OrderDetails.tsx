import { useState } from 'react'
import { Link } from 'react-router'
import type { Order } from '../../api/types'
import { Code, Disclosures, Fact, Facts, Inspector, Mono, Prose } from '../../components/graph/Inspector'
import PatchList from '../../components/shared/PatchList'
import { ButtonLink, Disclosure } from '../../components/ui'
import { paths } from '../../routes/paths'
import { formatDate, formatDuration, plural } from '../../utils/format'
import { orderDestinationLabel } from '../../utils/products'
import { objectToYaml } from '../../utils/yaml'
import layout from '../../components/layout/layout.module.css'
import { OrderIcon } from './orderIcons'
import { stateStatus, type OrderInsight } from './orderInsights'

interface Props {
  insight: OrderInsight
  onClose: () => void
}

/** What the Order runs and whether something newer waits, in a sentence. */
function deliveryText({ active, activeVersion, updateReady, order }: OrderInsight): string {
  const version = activeVersion ? `, version ${activeVersion}` : ''
  if (active && updateReady) return `Runs an older Preparation${version}. Its latest Preparation is not promoted yet.`
  if (active) return `Runs its latest Preparation${version}.`
  if (order.latestRevision) return 'Nothing is deployed yet: its latest Preparation is not promoted.'
  return 'Nothing is rendered yet.'
}

/** How new Preparations of the Order go live, e.g. "Manual, 2 approvals from sre". */
function promotionText(order: Order): string {
  if (!order.approvals) return order.mode
  const { requiredApprovals, allowedGroups } = order.approvals
  return `${order.mode}, ${plural(requiredApprovals, 'approval')} from ${allowedGroups.join(' or ')}`
}

/** The Orders the Order runs after, e.g. "podinfo-staging after a soak of 1 day". */
function upstreamText(order: Order): string {
  return (order.after ?? []).map((s) => (s.soakSeconds ? `${s.order} after a soak of ${formatDuration(s.soakSeconds)}` : s.order)).join(', ')
}

/** OrderDetails is the inspector of an Order: what it runs, what waits, and what it changes on its Product. */
export default function OrderDetails({ insight, onClose }: Props) {
  const [open, setOpen] = useState<string | null>(null)
  const { order } = insight
  const status = stateStatus(order.state)
  const ready = order.conditions?.find((c) => c.type === 'Ready')
  const values = objectToYaml(order.values ?? {})
  const patches = order.patches ?? []
  const edits = order.edits ?? []
  const toggle = (id: string) => (next: boolean) => setOpen(next ? id : null)

  const prepLink = (name?: string) =>
    name ? (
      <Link className={`${layout.mono} ${layout.inlineLink}`} to={paths.preparation(order.namespace, name)}>{name}</Link>
    ) : (
      <span className={layout.muted}>—</span>
    )

  return (
    <Inspector
      label={`Order ${order.name} details`}
      icon={<OrderIcon size="lg" muted={!insight.active} />}
      title={order.name}
      status={status}
      actions={<ButtonLink size="sm" to={paths.order(order.namespace, order.name)} aria-label={`Open Order ${order.name}`}>Open</ButtonLink>}
      onClose={onClose}
    >
      <Prose>
        {status.tone !== 'ok' && ready?.message && <p>{ready.message}</p>}
        <p>{deliveryText(insight)}</p>
      </Prose>

      <Facts>
        <Fact label="Running">{prepLink(order.activePreparation)}</Fact>
        {insight.activeVersion && <Fact label="Version">{insight.activeVersion}</Fact>}
        <Fact label="Latest">{prepLink(order.latestRevision)}</Fact>
        <Fact label="Promotion">{promotionText(order)}</Fact>
        {!!order.after?.length && <Fact label="Runs after">{upstreamText(order)}</Fact>}
        {insight.currentRevision && <Fact label="Revision">Runs the current Product revision</Fact>}
        <Fact label="Changes on the Product">{insight.overlay}</Fact>
        <Fact label="Destination"><Mono>{orderDestinationLabel(order)}</Mono></Fact>
        <Fact label="Created">{formatDate(order.createdAt)}</Fact>
      </Facts>

      {(values || patches.length > 0 || edits.length > 0) && (
        <Disclosures>
          {values && (
            <Disclosure tone="inline" label="Values" open={open === 'values'} onOpenChange={toggle('values')}>
              <Code>{values}</Code>
            </Disclosure>
          )}
          {patches.length > 0 && (
            <Disclosure tone="inline" label={`Patches · ${patches.length}`} open={open === 'patches'} onOpenChange={toggle('patches')}>
              <PatchList patches={patches} />
            </Disclosure>
          )}
          {edits.length > 0 && (
            <Disclosure tone="inline" label={`Edits · ${edits.length}`} open={open === 'edits'} onOpenChange={toggle('edits')}>
              <PatchList patches={edits} />
            </Disclosure>
          )}
        </Disclosures>
      )}
    </Inspector>
  )
}
