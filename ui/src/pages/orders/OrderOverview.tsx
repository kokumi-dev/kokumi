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
import { formatDate } from '../../utils/format'
import { orderDestinationLabel } from '../../utils/products'
import { objectToYaml } from '../../utils/yaml'
import { useOrderContext } from './orderContext'
import { useDeniedReason } from '../../hooks/useCapability'
import layout from '../../components/layout/layout.module.css'

export default function OrderOverview() {
  const { order, product, preparations } = useOrderContext()
  // Edits staged for removal; committed through the CommitBox below.
  const [pendingEdits, setPendingEdits] = useState<Patch[] | null>(null)
  const editDenied = useDeniedReason('orders:update')

  const edits = pendingEdits ?? order.edits ?? []
  const latest = preparations?.[0]

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
            { label: 'Latest Preparation', value: prepLink(order.latestRevision) },
            {
              label: 'Latest change',
              value: latest?.commitMessage?.trim() || <span className={layout.muted}>—</span>,
              show: !!latest,
            },
            { label: 'Promotion', value: order.mode },
            {
              label: 'Approvals',
              value: order.approvals
                ? `${order.approvals.requiredApprovals} required from ${order.approvals.allowedGroups.join(', ')}`
                : 'Not required',
            },
          ]}
        />
      </Section>

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
