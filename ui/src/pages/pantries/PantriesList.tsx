import type { Pantry } from '../../api/types'
import { usePantries } from '../../hooks/usePantries'
import { matchesFilter, useFilterParam } from '../../hooks/useFilterParam'
import PageHeader from '../../components/layout/PageHeader'
import Section from '../../components/layout/Section'
import DataTable from '../../components/layout/DataTable'
import EmptyState from '../../components/layout/EmptyState'
import FilterInput from '../../components/layout/FilterInput'
import NameCell from '../../components/layout/NameCell'
import StatusIndicator from '../../components/shared/StatusIndicator'
import RelativeTime from '../../components/shared/RelativeTime'
import { BtnLink } from '../../components/shared/Btn'
import { paths } from '../../routes/paths'
import { stripOCIScheme } from '../../utils/format'
import layout from '../../components/layout/layout.module.css'

export default function PantriesList() {
  const pantries = usePantries()
  const [query, setQuery] = useFilterParam()

  const filtered = (pantries ?? []).filter((p) => matchesFilter(query, p.name, p.namespace, p.url, p.description))

  return (
    <div className={layout.page}>
      <PageHeader
        title="Pantries"
        subtitle="OCI registries and the credentials used to pull sources and push Preparations"
        actions={<BtnLink variant="primary" to={paths.newPantry()}>New Pantry</BtnLink>}
      />

      <Section
        title="All Pantries"
        actions={<FilterInput label="Filter pantries" value={query} onChange={setQuery} placeholder="Filter by name or URL…" />}
        flush
      >
        {pantries === null ? (
          <EmptyState text="Loading…" />
        ) : filtered.length === 0 ? (
          <EmptyState
            text={query ? 'No pantries match your filter' : 'No pantries yet'}
            action={!query && <BtnLink variant="secondary" size="sm" to={paths.newPantry()}>Connect a registry</BtnLink>}
          />
        ) : (
          <DataTable<Pantry>
            rows={filtered}
            rowKey={(p) => `${p.namespace}/${p.name}`}
            rowHref={(p) => paths.pantry(p.namespace, p.name)}
            columns={[
              {
                header: 'Name',
                width: '24%',
                cell: (p) => <NameCell to={paths.pantry(p.namespace, p.name)} name={p.name} secondary={p.namespace} />,
              },
              { header: 'Status', width: '110px', cell: (p) => <StatusIndicator state={p.state} /> },
              { header: 'URL', truncate: true, title: (p) => p.url, cell: (p) => stripOCIScheme(p.url) },
              {
                header: 'Credentials',
                width: '18%',
                truncate: true,
                title: (p) => p.secretRef,
                cell: (p) => p.secretRef ?? <span className={layout.muted}>—</span>,
              },
              { header: 'Created', width: '110px', cell: (p) => <RelativeTime iso={p.createdAt} /> },
            ]}
          />
        )}
      </Section>
    </div>
  )
}
