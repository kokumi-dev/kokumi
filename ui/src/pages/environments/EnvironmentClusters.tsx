import { useMemo } from 'react'
import type { Cluster } from '../../api/types'
import Section from '../../components/layout/Section'
import DataTable from '../../components/layout/DataTable'
import EmptyState from '../../components/layout/EmptyState'
import NameCell from '../../components/layout/NameCell'
import StatusIndicator from '../../components/shared/StatusIndicator'
import { paths } from '../../routes/paths'
import { clusterTone } from '../../utils/clusters'
import { useEnvironmentContext } from './environmentContext'
import layout from '../../components/layout/layout.module.css'

export default function EnvironmentClusters() {
  const { environment, clusters } = useEnvironmentContext()
  const { namespace, name } = environment
  // The server works out which Environments select each Cluster, the way
  // the controllers do.
  const rows = useMemo(
    () => clusters?.filter((c) => c.environments?.includes(name)).sort((a, b) => a.name.localeCompare(b.name)) ?? null,
    [clusters, name],
  )
  const unknown = <span className={layout.muted}>—</span>

  return (
    <Section
      title="Clusters"
      description={`The Clusters of namespace ${namespace} it selects, and whether Orders can deploy to each`}
      flush
    >
      {rows === null ? (
        <EmptyState text="Loading…" />
      ) : rows.length === 0 ? (
        <EmptyState text="It selects no Cluster yet: Orders that deploy here have nowhere to deploy." />
      ) : (
        <DataTable<Cluster>
          rows={rows}
          rowKey={(c) => c.name}
          rowHref={(c) => paths.cluster(namespace, c.name)}
          columns={[
            {
              header: 'Cluster',
              width: '24%',
              cell: (c) => <NameCell to={paths.cluster(namespace, c.name)} name={c.name} secondary={c.description} />,
            },
            {
              header: 'State',
              wrap: true,
              cell: (c) => (
                <>
                  <StatusIndicator state={c.state} tone={clusterTone(c)} />
                  {c.message && <span className={layout.secondaryText}>{c.message}</span>}
                </>
              ),
            },
            {
              header: 'Server',
              width: '28%',
              truncate: true,
              title: (c) => c.server,
              cell: (c) => (c.server ? <span className={layout.mono}>{c.server}</span> : unknown),
            },
            { header: 'Version', width: '100px', cell: (c) => c.kubernetesVersion ?? unknown },
          ]}
        />
      )}
    </Section>
  )
}
