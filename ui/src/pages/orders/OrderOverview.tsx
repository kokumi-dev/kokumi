import { useState } from 'react'
import { Link } from 'react-router'
import type { Patch } from '../../api/types'
import { saveOrderEdits } from '../../api/client'
import Section from '../../components/layout/Section'
import PropertyList from '../../components/layout/PropertyList'
import ConditionList from '../../components/layout/ConditionList'
import PatchList from '../../components/shared/PatchList'
import CommitBox from '../../components/shared/CommitBox'
import RegistryAccessList from '../../components/shared/RegistryAccessList'
import { Button, Counter } from '../../components/ui'
import { paths } from '../../routes/paths'
import { formatDate, formatDuration } from '../../utils/format'
import { orderDestinationLabel, targetNamespaceSource } from '../../utils/products'
import { describeVulnerabilityCheck } from '../../utils/vulnerabilities'
import { objectToYaml } from '../../utils/yaml'
import { useOrderContext } from './orderContext'
import OrderPipeline from './OrderPipeline'
import { servingQueue } from '../../utils/promotion'
import { useDeniedReason } from '../../hooks/useCapability'
import layout from '../../components/layout/layout.module.css'

export default function OrderOverview() {
  const { order, product, preparations, servings, allOrders, allServings } = useOrderContext()
  // Edits staged for removal; committed through the CommitBox below.
  const [pendingEdits, setPendingEdits] = useState<Patch[] | null>(null)
  const editDenied = useDeniedReason('orders:update')

  const edits = pendingEdits ?? order.edits ?? []
  const latest = preparations?.[0]
  const serving = servings?.find((s) => s.name === order.name)
  const queued = order.mode === 'Manual' && serving ? servingQueue(serving) : undefined

  function removeEdit(index: number) {
    setPendingEdits(edits.filter((_, i) => i !== index))
  }

  function removeEditPath(index: number, path: string) {
    setPendingEdits(
      edits
        .map((e, i) => {
          if (i !== index) return e
          const set = { ...e.set }
          delete set[path]
          return { ...e, set }
        })
        .filter((e) => Object.keys(e.set).length > 0),
    )
  }

  const prepLink = (prepName?: string) =>
    prepName ? (
      <Link className={`${layout.mono} ${layout.inlineLink}`} to={paths.preparation(order.namespace, prepName)}>
        {prepName}
      </Link>
    ) : (
      <span className={layout.muted}>—</span>
    )

  return (
    <>
      <Section title="Delivery" description="What is live and what is waiting to be promoted">
        <PropertyList
          items={[
            { label: 'Active Preparation', value: prepLink(order.activePreparation) },
            {
              label: 'Next Preparation',
              value: queued && (
                <>
                  {prepLink(serving?.desiredPreparation)}{' '}
                  <span className={layout.muted} title={queued.message}>queued, waits for {queued.waitingFor}</span>
                </>
              ),
              show: !!queued,
            },
            { label: 'Latest Preparation', value: prepLink(order.latestRevision) },
            {
              label: 'Latest change',
              value: latest?.commitMessage?.trim() || <span className={layout.muted}>—</span>,
              show: !!latest,
            },
            {
              label: 'Target namespace',
              value: (
                <>
                  <span className={layout.mono}>{order.effectiveTargetNamespace}</span>
                  <span className={layout.muted}>, {targetNamespaceSource(order.targetNamespaceSource)}</span>
                </>
              ),
            },
            { label: 'Promotion', value: order.mode },
            {
              label: 'Runs after',
              value: order.after
                ?.map((s) => `${s.order}${s.soakSeconds ? `, after a soak of ${formatDuration(s.soakSeconds)}` : ''}`)
                .join('; '),
              show: !!order.after?.length,
            },
            {
              label: 'Approvals',
              value: order.approvals
                ? `${order.approvals.requiredApprovals} required from ${order.approvals.allowedGroups.join(', ')}`
                : 'Not required',
            },
            {
              label: 'Vulnerability check',
              value: order.checks?.vulnerabilities ? describeVulnerabilityCheck(order.checks.vulnerabilities) : 'Not required',
            },
          ]}
        />
      </Section>

      <OrderPipeline order={order} product={product} orders={allOrders} servings={allServings} latest={latest} />

      <Section title="Configuration" description="What this variant changes on top of its Product">
        <PropertyList
          items={[
            {
              label: 'Product',
              value: (
                <Link className={layout.inlineLink} to={paths.product(order.namespace, order.productRef.name)}>
                  {order.productRef.name}
                </Link>
              ),
            },
            {
              label: 'Source',
              value: product && (
                <span className={layout.mono}>{product.source.url}</span>
              ),
              show: !!product,
            },
            { label: 'Version', value: product?.source.version, show: !!product },
            { label: 'Destination', value: <span className={layout.mono}>{orderDestinationLabel(order)}</span> },
            {
              label: 'Registries',
              value: order.registryRefs?.length
                ? order.registryRefs.map((r) => r.name).join(', ')
                : <span className={layout.muted}>None: the destination is written anonymously</span>,
            },
            {
              label: 'Values',
              value: <pre className={layout.code}>{objectToYaml(order.values ?? {})}</pre>,
              show: Object.keys(order.values ?? {}).length > 0,
            },
            { label: 'Created', value: formatDate(order.createdAt) },
          ]}
        />
      </Section>

      {order.patches && order.patches.length > 0 && (
        <Section title={<>Patches<Counter count={order.patches.length} /></>} description="Declared in the Order spec">
          <PatchList patches={order.patches} />
        </Section>
      )}

      {(edits.length > 0 || pendingEdits !== null) && (
        <Section
          title={<>Edits<Counter count={edits.length} /></>}
          description="Field changes made on the rendered manifest"
          actions={edits.length > 0 && (
            <Button variant="danger" size="sm" onClick={() => setPendingEdits([])} disabledReason={editDenied}>Clear all</Button>
          )}
        >
          <div className={layout.stack}>
            {edits.length > 0 ? (
              <PatchList
                patches={edits}
                onRemove={editDenied ? undefined : removeEdit}
                onRemovePath={editDenied ? undefined : removeEditPath}
              />
            ) : (
              <span className={layout.muted}>All edits will be removed.</span>
            )}
            {pendingEdits !== null && (
              <CommitBox
                title="Commit edit removal"
                onCancel={() => setPendingEdits(null)}
                onCommit={async (message) => {
                  await saveOrderEdits(order.namespace, order.name, pendingEdits, message)
                  setPendingEdits(null)
                }}
              />
            )}
          </div>
        </Section>
      )}

      <Section
        title="Registry access"
        description="The locations this Order and its images need, who pulls or pushes them, and the Registry Kokumi connects through"
        flush
      >
        <RegistryAccessList access={order.registryAccess} namespace={order.namespace} />
      </Section>

      <Section title="Conditions">
        <ConditionList conditions={order.conditions} />
      </Section>
    </>
  )
}
