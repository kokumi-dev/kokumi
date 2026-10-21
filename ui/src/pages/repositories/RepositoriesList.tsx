import { Link } from 'react-router'
import type { Repository } from '../../api/types'
import { useRepositories } from '../../hooks/useRegistries'
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
import { stateTone } from '../../utils/state'
import layout from '../../components/layout/layout.module.css'

export default function RepositoriesList() {
  const repositories = useRepositories()
  const [query, setQuery] = useFilterParam()
  const createDenied = useDeniedReason('repositories:create')

  const filtered = (repositories ?? []).filter((r) => matchesFilter(query, r.name, r.namespace, r.url, r.description))

  return (
    <div className={layout.page}>
      <PageHeader
        title="Repositories"
        subtitle="A catalog of OCI repositories to browse and create Products from"
        actions={<ButtonLink variant="primary" icon={<PlusIcon />} to={paths.newRepository()} disabledReason={createDenied}>New Repository</ButtonLink>}
      />

      <Section
        title="All Repositories"
        actions={<FilterInput label="Filter repositories" value={query} onChange={setQuery} placeholder="Filter by name or URL…" />}
        flush
      >
        {repositories === null ? (
          <EmptyState text="Loading…" />
        ) : filtered.length === 0 ? (
          <EmptyState
            text={query ? 'No repositories match your filter' : 'No repositories yet'}
            action={!query && <ButtonLink variant="secondary" size="sm" to={paths.newRepository()} disabledReason={createDenied}>Add a repository</ButtonLink>}
          />
        ) : (
          <DataTable<Repository>
            rows={filtered}
            rowKey={(r) => `${r.namespace}/${r.name}`}
            rowHref={(r) => paths.repository(r.namespace, r.name)}
            columns={[
              {
                header: 'Name',
                width: '24%',
                cell: (r) => <NameCell to={paths.repository(r.namespace, r.name)} name={r.name} secondary={r.namespace} />,
              },
              { header: 'Status', width: '130px', cell: (r) => <StatusIndicator state={r.state} tone={stateTone(r)} /> },
              { header: 'URL', truncate: true, title: (r) => r.url, cell: (r) => stripOCIScheme(r.url) },
              {
                header: 'Registry',
                width: '18%',
                truncate: true,
                title: (r) => r.registryRef,
                cell: (r) => r.registryRef
                  ? <Link to={paths.registry(r.namespace, r.registryRef)} onClick={(e) => e.stopPropagation()}>{r.registryRef}</Link>
                  : <span className={layout.muted}>Anonymous</span>,
              },
              { header: 'Created', width: '110px', cell: (r) => <RelativeTime iso={r.createdAt} /> },
            ]}
          />
        )}
      </Section>
    </div>
  )
}
