import { useState } from 'react'
import { Link } from 'react-router'
import type { Patch } from '../../api/types'
import { saveOrderEdits } from '../../api/client'
import Section from '../../components/layout/Section'
import PropertyList from '../../components/layout/PropertyList'
import ConditionList from '../../components/layout/ConditionList'
import PatchList from '../../components/shared/PatchList'
import CommitBox from '../../components/shared/CommitBox'
import { Button, Counter } from '../../components/ui'
import { paths } from '../../routes/paths'
import { formatDate } from '../../utils/format'
import { useOrderContext } from './orderContext'
import layout from '../../components/layout/layout.module.css'

export default function OrderOverview() {
  const { order, preparations } = useOrderContext()
  // Edits staged for removal; committed through the CommitBox below.
  const [pendingEdits, setPendingEdits] = useState<Patch[] | null>(null)

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

  const destination = order.destination?.pantryRef && !order.destination.oci
    ? `pantry: ${order.destination.pantryRef.name}`
    : order.destination?.oci || `${order.effectiveDestination ?? ''} (default)`

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

      <Section title="Configuration">
        <PropertyList
          items={[
            {
              label: 'Menu',
              value: order.menuRef && (
                <Link className={layout.inlineLink} to={paths.menu(order.namespace, order.menuRef.name)}>
                  {order.menuRef.name}
                </Link>
              ),
              show: !!order.menuRef,
            },
            {
              label: 'Source',
              value: (
                <span className={layout.mono}>
                  {order.source?.pantryRef && !order.source.oci ? `pantry: ${order.source.pantryRef.name}` : order.source?.oci}
                </span>
              ),
              show: !!order.source,
            },
            { label: 'Version', value: order.source?.version, show: !!order.source },
            { label: 'Destination', value: <span className={layout.mono}>{destination}</span> },
            { label: 'Renderer', value: order.render?.helm ? 'Helm' : 'Manifest', show: !!order.render },
            { label: 'Release name', value: order.render?.helm?.releaseName, show: !!order.render?.helm?.releaseName },
            { label: 'Helm namespace', value: order.render?.helm?.namespace, show: !!order.render?.helm?.namespace },
            { label: 'Include CRDs', value: order.render?.helm?.includeCRDs ? 'Yes' : 'No', show: !!order.render?.helm },
            { label: 'File layout', value: order.render?.manifest?.layout, show: !!order.render?.manifest },
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
            <Button variant="danger" size="sm" onClick={() => setPendingEdits([])}>Clear all</Button>
          )}
        >
          <div className={layout.stack}>
            {edits.length > 0 ? (
              <PatchList patches={edits} onRemove={removeEdit} onRemovePath={removeEditPath} />
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

      <Section title="Conditions">
        <ConditionList conditions={order.conditions} />
      </Section>
    </>
  )
}
