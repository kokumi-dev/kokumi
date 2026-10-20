import { Link } from 'react-router'
import type { Condition } from '../../api/types'
import Section from '../../components/layout/Section'
import PropertyList from '../../components/layout/PropertyList'
import ConditionList from '../../components/layout/ConditionList'
import RelativeTime from '../../components/shared/RelativeTime'
import VulnerabilityCounts from '../../components/vulnerability/VulnerabilityCounts'
import { paths } from '../../routes/paths'
import { formatDate } from '../../utils/format'
import { deployedEntry, servingQueue, shortRevision } from '../../utils/promotion'
import { useOrders } from '../../hooks/useOrders'
import { useServingContext } from './servingContext'
import layout from '../../components/layout/layout.module.css'

/** How a gate condition reads in a row: passed, else its reason and message. */
function gateText(condition: Condition | undefined, passed: string): string {
  if (!condition) return '—'
  return condition.status === 'True' ? passed : `${condition.reason}: ${condition.message}`
}

export default function ServingOverview() {
  const { serving } = useServingContext()
  const orders = useOrders()
  const manual = orders?.find((o) => o.namespace === serving.namespace && o.name === serving.order)?.mode === 'Manual'
  const queued = manual ? servingQueue(serving) : undefined
  const target = serving.targetPreparation || serving.desiredPreparation
  const inSync = !!target && target === serving.deployedPreparation
  const deployed = deployedEntry(serving)
  const condition = (type: string) => serving.conditions?.find((c) => c.type === type)
  const gate = condition('Approved')
  const verified = condition('Verified')
  const upstream = condition('UpstreamDeployed')
  const checks = condition('ChecksPassed')

  const prepLink = (prepName?: string) =>
    prepName ? (
      <Link className={`${layout.mono} ${layout.inlineLink}`} to={paths.preparation(serving.namespace, prepName)}>{prepName}</Link>
    ) : (
      <span className={layout.muted}>—</span>
    )

  return (
    <>
      <Section title="Deployment" description={inSync ? 'The deployed Preparation matches the target.' : 'Waiting for the target Preparation to be deployed.'}>
        <PropertyList
          items={[
            {
              label: 'Order',
              value: <Link className={layout.inlineLink} to={paths.order(serving.namespace, serving.order)}>{serving.order}</Link>,
            },
            { label: 'Target Preparation', value: prepLink(target) },
            { label: 'Deployed Preparation', value: prepLink(serving.deployedPreparation) },
            {
              label: 'Next Preparation',
              value: queued && (
                <>
                  {prepLink(serving.desiredPreparation)}{' '}
                  <span className={layout.muted} title={queued.message}>queued, waits for {queued.waitingFor}</span>
                </>
              ),
              show: !!queued,
            },
            {
              label: 'Deployed digest',
              value: <span className={layout.mono}>{serving.deployedDigest}</span>,
              show: !!serving.deployedDigest,
            },
            {
              label: 'Product revision',
              value: <span className={layout.mono} title={deployed?.productRevision}>{shortRevision(deployed?.productRevision)}</span>,
              show: !!deployed,
            },
            {
              label: 'Healthy since',
              value: <RelativeTime iso={deployed?.healthyAt} />,
              show: !!deployed?.healthyAt && !deployed.endedAt,
            },
            { label: 'Upstream stages', value: gateText(upstream, 'Deployed upstream'), show: !!upstream },
            { label: 'Vulnerability check', value: gateText(checks, 'Passed'), show: !!checks },
            { label: 'Approval gate', value: gate?.reason ?? '—', show: !!gate },
            { label: 'Signatures', value: gateText(verified, 'Verified in the registry'), show: !!verified },
            {
              label: 'Deployed findings',
              value: serving.deployedVulnerabilities && <VulnerabilityCounts summary={serving.deployedVulnerabilities} />,
              show: !!serving.deployedVulnerabilities,
            },
            { label: 'Created', value: formatDate(serving.createdAt) },
          ]}
        />
      </Section>

      <Section title="Conditions">
        <ConditionList conditions={serving.conditions} />
      </Section>
    </>
  )
}
