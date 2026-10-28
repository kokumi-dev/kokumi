import { Link } from 'react-router'
import type { Cluster } from '../../api/types'
import { useClusters } from '../../hooks/useEnvironments'
import { matchesFilter, useFilterParam } from '../../hooks/useFilterParam'
import { useDeniedReason } from '../../hooks/useCapability'
import PageHeader from '../../components/layout/PageHeader'
import Section from '../../components/layout/Section'
import DataTable from '../../components/layout/DataTable'
import EmptyState from '../../components/layout/EmptyState'
import FilterInput from '../../components/layout/FilterInput'
import NameCell from '../../components/layout/NameCell'
import StatusIndicator from '../../components/shared/StatusIndicator'
import { ButtonLink, PlusIcon } from '../../components/ui'
import { paths } from '../../routes/paths'
import { clusterTone, connectionLabel } from '../../utils/clusters'
import layout from '../../components/layout/layout.module.css'

export default function ClustersList() {
  const clusters = useClusters()
  const [query, setQuery] = useFilterParam()
  const createDenied = useDeniedReason('clusters:create')

  const filtered = (clusters ?? []).filter((c) => matchesFilter(query, c.name, c.namespace, c.description))

  return (
    <div className={layout.page}>
      <PageHeader
        title="Clusters"
        subtitle="The clusters Orders deploy to: the Environments of a namespace select its Clusters by their labels"
        actions={<ButtonLink variant="primary" icon={<PlusIcon />} to={paths.newCluster()} disabledReason={createDenied}>New Cluster</ButtonLink>}
      />

      <Section
        title="All Clusters"
        actions={<FilterInput label="Filter clusters" value={query} onChange={setQuery} placeholder="Filter by name or namespace…" />}
        flush
      >
        {clusters === null ? (
          <EmptyState text="Loading…" />
        ) : filtered.length === 0 ? (
          <EmptyState
            text={query ? 'No clusters match your filter' : 'No clusters yet: Kokumi keeps the cluster it runs in as the Cluster in-cluster of its namespace'}
            action={!query && <ButtonLink variant="secondary" size="sm" to={paths.newCluster()} disabledReason={createDenied}>Create a Cluster</ButtonLink>}
          />
        ) : (
          <DataTable<Cluster>
            rows={filtered}
            rowKey={(c) => `${c.namespace}/${c.name}`}
            rowHref={(c) => paths.cluster(c.namespace, c.name)}
            columns={[
              {
                header: 'Cluster',
                width: '20%',
                cell: (c) => <NameCell to={paths.cluster(c.namespace, c.name)} name={c.name} secondary={c.namespace} />,
              },
              {
                header: 'Connection',
                width: '130px',
                title: (c) => (c.kubeconfigSecret ? `Kubeconfig in Secret ${c.kubeconfigSecret}` : undefined),
                cell: (c) => connectionLabel(c),
              },
              {
                header: 'Server',
                truncate: true,
                title: (c) => c.server,
                cell: (c) => (c.server ? <span className={layout.mono}>{c.server}</span> : <span className={layout.muted}>—</span>),
              },
              {
                header: 'Version',
                width: '90px',
                cell: (c) => c.kubernetesVersion ?? <span className={layout.muted}>—</span>,
              },
              {
                header: 'State',
                width: '150px',
                truncate: true,
                title: (c) => c.message,
                cell: (c) => <StatusIndicator state={c.state} tone={clusterTone(c)} />,
              },
              {
                header: 'Environments',
                width: '16%',
                wrap: true,
                cell: (c) =>
                  c.environments?.length ? (
                    c.environments.map((e, i) => (
                      <span key={e}>
                        {i > 0 && ', '}
                        <Link className={layout.refLink} to={paths.environment(c.namespace, e)}>{e}</Link>
                      </span>
                    ))
                  ) : (
                    <span className={layout.muted}>None</span>
                  ),
              },
            ]}
          />
        )}
      </Section>
    </div>
  )
}
