import { Link } from 'react-router'
import type { ClusterDeployment } from '../../api/types'
import { argoAppURL } from '../../hooks/useArgoCDBase'
import { useClusterDeployments } from '../../hooks/useEnvironments'
import Section from '../../components/layout/Section'
import DataTable from '../../components/layout/DataTable'
import EmptyState from '../../components/layout/EmptyState'
import NameCell from '../../components/layout/NameCell'
import StatusIndicator from '../../components/shared/StatusIndicator'
import RelativeTime from '../../components/shared/RelativeTime'
import { paths } from '../../routes/paths'
import { targetTone } from '../../utils/clusters'
import { useClusterContext } from './clusterContext'
import layout from '../../components/layout/layout.module.css'

/** ClusterDeployments lists what runs on a Cluster: what each Order of the Environments that select it deploys there. */
export default function ClusterDeployments() {
  const { cluster, argoBase } = useClusterContext()
  const { namespace } = cluster
  const deployments = useClusterDeployments(namespace, cluster.name)

  return (
    <Section
      title="Deployments"
      description={`The Orders of namespace ${namespace} that deploy here, one Argo CD Application each, and what Argo CD reports`}
      flush
    >
      {deployments.error ? (
        <EmptyState text={`Could not read what deploys here: ${deployments.error}`} />
      ) : deployments.data === null ? (
        <EmptyState text="Loading…" />
      ) : deployments.data.length === 0 ? (
        <EmptyState text="Nothing deploys here yet: Orders deploy to the Clusters of their Environment once a Preparation is promoted." />
      ) : (
        <DataTable<ClusterDeployment>
          rows={deployments.data}
          rowKey={(d) => d.order}
          // The Serving carries its Order's name, and exists from the first promotion.
          rowHref={(d) => (d.preparation ? paths.serving(namespace, d.order) : paths.order(namespace, d.order))}
          columns={[
            {
              header: 'Order',
              width: '22%',
              cell: (d) => <NameCell to={paths.order(namespace, d.order)} name={d.order} secondary={d.environment} />,
            },
            {
              header: 'Deployed Preparation',
              width: '24%',
              truncate: true,
              title: (d) => d.preparation,
              cell: (d) =>
                d.preparation ? (
                  <Link className={layout.refLink} to={paths.preparation(namespace, d.preparation)}>
                    {d.preparation}
                  </Link>
                ) : (
                  <span className={layout.muted}>—</span>
                ),
            },
            {
              header: 'State',
              wrap: true,
              cell: (d) => (
                <>
                  <StatusIndicator state={d.state} tone={targetTone(d)} />
                  {d.message && <span className={layout.secondaryText}>{d.message}</span>}
                </>
              ),
            },
            { header: 'Since', width: '110px', cell: (d) => <RelativeTime iso={d.since} /> },
            {
              header: <span className={layout.visuallyHidden}>Argo CD</span>,
              width: '120px',
              alignEnd: true,
              truncate: true,
              title: (d) => d.applicationName,
              cell: (d) => {
                if (!d.applicationName) return <span className={layout.muted}>—</span>
                const url = argoAppURL(argoBase, d.applicationName)
                return url ? (
                  <a className={layout.inlineLink} href={url} target="_blank" rel="noopener noreferrer" aria-label={`Open ${d.applicationName} in Argo CD`}>
                    Argo CD ↗
                  </a>
                ) : (
                  <span className={`${layout.mono} ${layout.muted}`}>{d.applicationName}</span>
                )
              },
            },
          ]}
        />
      )}
    </Section>
  )
}
