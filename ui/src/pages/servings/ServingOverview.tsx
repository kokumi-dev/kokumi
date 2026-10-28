import { Link } from 'react-router'
import type { Condition, DeploymentTarget } from '../../api/types'
import { argoAppURL } from '../../hooks/useArgoCDBase'
import Section from '../../components/layout/Section'
import PropertyList from '../../components/layout/PropertyList'
import ConditionList from '../../components/layout/ConditionList'
import DataTable from '../../components/layout/DataTable'
import EmptyState from '../../components/layout/EmptyState'
import NameCell from '../../components/layout/NameCell'
import StatusIndicator from '../../components/shared/StatusIndicator'
import RelativeTime from '../../components/shared/RelativeTime'
import VulnerabilityCounts from '../../components/vulnerability/VulnerabilityCounts'
import { paths } from '../../routes/paths'
import { formatDate, shortDigest } from '../../utils/format'
import { targetTone, unhealthyCounts } from '../../utils/clusters'
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
  const { serving, targets, argoBase } = useServingContext()
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
  const counts = serving.clusters
  const unhealthy = counts ? unhealthyCounts(counts) : ''

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

      <Section
        title="Clusters"
        description={
          counts?.selected
            ? `${counts.healthy} of ${counts.selected} ${counts.selected === 1 ? 'cluster runs' : 'clusters run'} ${serving.deployedPreparation ?? 'the deployed Preparation'} healthily${unhealthy ? `; ${unhealthy}` : ''}. One Argo CD Application per cluster.`
            : 'One Argo CD Application per cluster, and what Argo CD reports on each'
        }
        flush
      >
        {targets.error ? (
          <EmptyState text={`Could not read the clusters: ${targets.error}`} />
        ) : targets.data === null ? (
          <EmptyState text="Loading…" />
        ) : targets.data.length === 0 ? (
          <EmptyState text="No clusters yet: they are listed once a Preparation is promoted." />
        ) : (
          <DataTable<DeploymentTarget>
            rows={targets.data}
            rowKey={(t) => t.cluster}
            columns={[
              {
                header: 'Cluster',
                width: '24%',
                cell: (t) => <NameCell to={paths.cluster(serving.namespace, t.cluster)} name={t.cluster} secondary={t.server} />,
              },
              {
                header: 'State',
                wrap: true,
                cell: (t) => (
                  <>
                    <StatusIndicator state={t.state} tone={targetTone(t)} />
                    {t.message && <span className={layout.secondaryText}>{t.message}</span>}
                  </>
                ),
              },
              {
                header: 'Revision',
                width: '180px',
                title: (t) => t.revision,
                cell: (t) =>
                  t.revision ? (
                    <span className={layout.mono} title={t.revision}>
                      {shortDigest(t.revision)}
                      {serving.deployedDigest && t.revision !== serving.deployedDigest && <span className={layout.muted}> (previous)</span>}
                    </span>
                  ) : (
                    <span className={layout.muted}>—</span>
                  ),
              },
              { header: 'Since', width: '110px', cell: (t) => <RelativeTime iso={t.since} /> },
              {
                header: <span className={layout.visuallyHidden}>Argo CD</span>,
                width: '120px',
                alignEnd: true,
                truncate: true,
                title: (t) => t.applicationName,
                cell: (t) => {
                  const url = argoAppURL(argoBase, t.applicationName)
                  return url ? (
                    <a className={layout.inlineLink} href={url} target="_blank" rel="noopener noreferrer" aria-label={`Open ${t.applicationName} in Argo CD`}>
                      Argo CD ↗
                    </a>
                  ) : (
                    <span className={`${layout.mono} ${layout.muted}`}>{t.applicationName}</span>
                  )
                },
              },
            ]}
          />
        )}
      </Section>

      <Section title="Conditions">
        <ConditionList conditions={serving.conditions} />
      </Section>
    </>
  )
}
