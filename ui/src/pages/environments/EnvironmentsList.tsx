import { useMemo } from 'react'
import type { Environment } from '../../api/types'
import { useEnvironments } from '../../hooks/useEnvironments'
import { useOrders } from '../../hooks/useOrders'
import { matchesFilter, useFilterParam } from '../../hooks/useFilterParam'
import { useDeniedReason } from '../../hooks/useCapability'
import PageHeader from '../../components/layout/PageHeader'
import Section from '../../components/layout/Section'
import DataTable from '../../components/layout/DataTable'
import EmptyState from '../../components/layout/EmptyState'
import FilterInput from '../../components/layout/FilterInput'
import NameCell from '../../components/layout/NameCell'
import StatusIndicator from '../../components/shared/StatusIndicator'
import RelativeTime from '../../components/shared/RelativeTime'
import { ButtonLink, PlusIcon } from '../../components/ui'
import { paths } from '../../routes/paths'
import { describeSelector } from '../../utils/selectors'
import { environmentClustersSummary } from '../../utils/clusters'
import { stateTone } from '../../utils/state'
import layout from '../../components/layout/layout.module.css'

export default function EnvironmentsList() {
  const environments = useEnvironments()
  const orders = useOrders()
  const [query, setQuery] = useFilterParam()
  const createDenied = useDeniedReason('environments:create')

  const filtered = (environments ?? []).filter((e) => matchesFilter(query, e.name, e.namespace, e.description))
  // The Orders of each Environment, counted once per list rather than per row.
  const orderCounts = useMemo(() => {
    const counts = new Map<string, number>()
    for (const o of orders ?? []) {
      const key = `${o.namespace}/${o.environmentRef.name}`
      counts.set(key, (counts.get(key) ?? 0) + 1)
    }
    return counts
  }, [orders])
  const orderCount = (env: Environment) => (orders === null ? undefined : orderCounts.get(`${env.namespace}/${env.name}`) ?? 0)
  const selector = (env: Environment) => describeSelector(env.clusterSelector, 'Every Cluster of the namespace')
  const requires = (env: Environment) => (env.promotion?.requireAfter ?? []).map((r) => r.environment).join(', ')

  return (
    <div className={layout.page}>
      <PageHeader
        title="Environments"
        subtitle="Groups of the Clusters of a namespace that its Orders deploy to, and what an Order must pass first"
        actions={<ButtonLink variant="primary" icon={<PlusIcon />} to={paths.newEnvironment()} disabledReason={createDenied}>New Environment</ButtonLink>}
      />

      <Section
        title="All Environments"
        actions={<FilterInput label="Filter environments" value={query} onChange={setQuery} placeholder="Filter by name or namespace…" />}
        flush
      >
        {environments === null ? (
          <EmptyState text="Loading…" />
        ) : filtered.length === 0 ? (
          <EmptyState
            text={query ? 'No environments match your filter' : 'No environments yet: every Order deploys to an Environment of its namespace'}
            action={!query && <ButtonLink variant="secondary" size="sm" to={paths.newEnvironment()} disabledReason={createDenied}>Create an Environment</ButtonLink>}
          />
        ) : (
          <DataTable<Environment>
            rows={filtered}
            rowKey={(e) => `${e.namespace}/${e.name}`}
            rowHref={(e) => paths.environment(e.namespace, e.name)}
            columns={[
              {
                header: 'Name',
                width: '20%',
                cell: (e) => <NameCell to={paths.environment(e.namespace, e.name)} name={e.name} secondary={e.namespace} />,
              },
              { header: 'Status', width: '130px', cell: (e) => <StatusIndicator state={e.state} tone={stateTone(e)} /> },
              {
                header: 'Clusters',
                truncate: true,
                title: (e) => selector(e),
                cell: (e) => (
                  <>
                    {environmentClustersSummary(e)}
                    <span className={layout.secondaryText}>{selector(e)}</span>
                  </>
                ),
              },
              {
                header: 'Argo CD project',
                width: '180px',
                truncate: true,
                title: (e) => e.deployer.argoCD?.project,
                cell: (e) => e.deployer.argoCD?.project ?? <span className={layout.muted}>—</span>,
              },
              {
                header: 'Requires',
                width: '14%',
                truncate: true,
                title: (e) => requires(e),
                cell: (e) => requires(e) || <span className={layout.muted}>—</span>,
              },
              { header: 'Orders', width: '96px', cell: (e) => orderCount(e) ?? '…' },
              { header: 'Created', width: '110px', cell: (e) => <RelativeTime iso={e.createdAt} /> },
            ]}
          />
        )}
      </Section>
    </div>
  )
}
