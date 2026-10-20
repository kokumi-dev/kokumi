import { Link } from 'react-router'
import type { DeploymentEntry } from '../../api/types'
import Section from '../../components/layout/Section'
import EmptyState from '../../components/layout/EmptyState'
import DataTable from '../../components/layout/DataTable'
import Badge from '../../components/shared/Badge'
import RelativeTime from '../../components/shared/RelativeTime'
import { paths } from '../../routes/paths'
import { shortDigest } from '../../utils/format'
import { shortRevision } from '../../utils/promotion'
import { useServingContext } from './servingContext'
import layout from '../../components/layout/layout.module.css'

/** How a deployment ended, or that it runs. */
function endText(e: DeploymentEntry) {
  if (!e.promotionRecord) return <Badge state="Recording" tone="neutral" title="The promotion is being recorded" />
  if (!e.endedAt) return <Badge state="Running" tone={e.healthyAt ? 'success' : 'neutral'} />
  return (
    <>
      <Badge state={e.endReason ?? 'Ended'} tone={e.endReason === 'Degraded' ? 'danger' : 'neutral'} /> <RelativeTime iso={e.endedAt} />
    </>
  )
}

export default function ServingHistory() {
  const { serving } = useServingContext()
  const history = serving.history ?? []

  // The records are referrers of the Preparation's artifact, shown verified there.
  const record = (e: DeploymentEntry, digest: string | undefined, label: string) =>
    digest && (
      <Link className={layout.refLink} title={digest} to={paths.preparation(serving.namespace, e.preparation, 'artifact')}>
        {label} {shortDigest(digest)}
      </Link>
    )

  return (
    <Section
      title="History"
      description="The most recent deployments, newest first. Each promotion and each healthy deployment is recorded, signed, as a referrer of its artifact."
      flush
    >
      {history.length === 0 ? (
        <EmptyState text="Nothing was deployed yet." />
      ) : (
        <DataTable<DeploymentEntry>
          rows={history}
          rowKey={(e) => `${e.preparation}@${e.startedAt}`}
          columns={[
            {
              header: 'Preparation',
              truncate: true,
              title: (e) => e.preparation,
              cell: (e) => (
                <Link className={layout.refLink} to={paths.preparation(serving.namespace, e.preparation)}>{e.preparation}</Link>
              ),
            },
            {
              header: 'Revision',
              width: '120px',
              title: (e) => e.productRevision,
              cell: (e) => <span className={layout.mono}>{shortRevision(e.productRevision)}</span>,
            },
            { header: 'Started', width: '110px', cell: (e) => <RelativeTime iso={e.startedAt} /> },
            { header: 'Healthy', width: '110px', cell: (e) => (e.healthyAt ? <RelativeTime iso={e.healthyAt} /> : <span className={layout.muted}>—</span>) },
            { header: 'Ended', width: '210px', cell: endText },
            {
              header: 'Records',
              width: '180px',
              cell: (e) => (
                <span className={layout.stack}>
                  {record(e, e.promotionRecord, 'Promotion')}
                  {record(e, e.deploymentRecord, 'Deployment')}
                </span>
              ),
            },
          ]}
        />
      )}
    </Section>
  )
}
