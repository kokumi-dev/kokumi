import { Link } from 'react-router'
import type { Serving } from '../../api/types'
import { useServings } from '../../hooks/useServings'
import { matchesFilter, useFilterParam } from '../../hooks/useFilterParam'
import PageHeader from '../../components/layout/PageHeader'
import Section from '../../components/layout/Section'
import DataTable from '../../components/layout/DataTable'
import EmptyState from '../../components/layout/EmptyState'
import FilterInput from '../../components/layout/FilterInput'
import NameCell from '../../components/layout/NameCell'
import StatusIndicator from '../../components/shared/StatusIndicator'
import RelativeTime from '../../components/shared/RelativeTime'
import { paths } from '../../routes/paths'
import layout from '../../components/layout/layout.module.css'

function syncLabel(s: Serving): string {
  const target = s.targetPreparation || s.desiredPreparation
  if (!target) return '—'
  return target === s.observedPreparation ? 'In sync' : 'Rolling out'
}

export default function ServingsList() {
  const servings = useServings()
  const [query, setQuery] = useFilterParam()

  const filtered = (servings ?? []).filter((s) => matchesFilter(query, s.name, s.namespace, s.order))

  return (
    <div className={layout.page}>
      <PageHeader title="Servings" subtitle="What is deployed right now, per Order" />

      <Section
        title="All Servings"
        actions={<FilterInput label="Filter servings" value={query} onChange={setQuery} placeholder="Filter by name or order…" />}
        flush
      >
        {servings === null ? (
          <EmptyState text="Loading…" />
        ) : filtered.length === 0 ? (
          <EmptyState text={query ? 'No servings match your filter' : 'No servings yet'} />
        ) : (
          <DataTable<Serving>
            rows={filtered}
            rowKey={(s) => `${s.namespace}/${s.name}`}
            rowHref={(s) => paths.serving(s.namespace, s.name)}
            columns={[
              {
                header: 'Name',
                width: '24%',
                cell: (s) => <NameCell to={paths.serving(s.namespace, s.name)} name={s.name} secondary={s.namespace} />,
              },
              { header: 'Status', width: '110px', cell: (s) => <StatusIndicator state={s.state} /> },
              {
                header: 'Order',
                truncate: true,
                title: (s) => s.order,
                cell: (s) => <Link className={layout.refLink} to={paths.order(s.namespace, s.order)}>{s.order}</Link>,
              },
              {
                header: 'Deployed',
                width: '26%',
                truncate: true,
                title: (s) => s.observedPreparation,
                cell: (s) =>
                  s.observedPreparation ? (
                    <Link className={layout.refLink} to={paths.preparation(s.namespace, s.observedPreparation)}>
                      {s.observedPreparation}
                    </Link>
                  ) : (
                    <span className={layout.muted}>—</span>
                  ),
              },
              {
                header: 'Sync',
                width: '110px',
                title: (s) => s.targetPreparation || s.desiredPreparation,
                truncate: true,
                cell: syncLabel,
              },
              { header: 'Created', width: '110px', cell: (s) => <RelativeTime iso={s.createdAt} /> },
            ]}
          />
        )}
      </Section>
    </div>
  )
}
