import type { Registry } from '../../api/types'
import { useRegistries } from '../../hooks/useRegistries'
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
import { stripOCIScheme } from '../../utils/format'
import layout from '../../components/layout/layout.module.css'

export default function RegistriesList() {
  const registries = useRegistries()
  const [query, setQuery] = useFilterParam()
  const createDenied = useDeniedReason('registries:create')

  const filtered = (registries ?? []).filter((r) => matchesFilter(query, r.name, r.namespace, r.url, r.secretRef))

  return (
    <div className={layout.page}>
      <PageHeader
        title="Registries"
        subtitle="How Kokumi connects to OCI registries: credentials, TLS and scheme for the locations below a URL"
        actions={<ButtonLink variant="primary" icon={<PlusIcon />} to={paths.newRegistry()} disabledReason={createDenied}>New Registry</ButtonLink>}
      />

      <Section
        title="All Registries"
        actions={<FilterInput label="Filter registries" value={query} onChange={setQuery} placeholder="Filter by name, URL or Secret…" />}
        flush
      >
        {registries === null ? (
          <EmptyState text="Loading…" />
        ) : filtered.length === 0 ? (
          <EmptyState
            text={query ? 'No registries match your filter' : 'No registries yet: Kokumi reads and writes anonymously'}
            action={!query && <ButtonLink variant="secondary" size="sm" to={paths.newRegistry()} disabledReason={createDenied}>Connect a registry</ButtonLink>}
          />
        ) : (
          <DataTable<Registry>
            rows={filtered}
            rowKey={(r) => `${r.namespace}/${r.name}`}
            rowHref={(r) => paths.registry(r.namespace, r.name)}
            columns={[
              {
                header: 'Name',
                width: '24%',
                cell: (r) => <NameCell to={paths.registry(r.namespace, r.name)} name={r.name} secondary={r.namespace} />,
              },
              { header: 'Status', width: '130px', cell: (r) => <StatusIndicator state={r.state} /> },
              { header: 'URL', truncate: true, title: (r) => r.url, cell: (r) => stripOCIScheme(r.url) },
              {
                header: 'Credentials',
                width: '18%',
                truncate: true,
                title: (r) => r.secretRef,
                cell: (r) => r.secretRef ?? <span className={layout.muted}>Anonymous</span>,
              },
              { header: 'Scheme', width: '100px', cell: (r) => r.scheme },
              { header: 'Created', width: '110px', cell: (r) => <RelativeTime iso={r.createdAt} /> },
            ]}
          />
        )}
      </Section>
    </div>
  )
}
