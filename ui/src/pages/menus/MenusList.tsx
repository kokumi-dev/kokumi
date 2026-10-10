import type { Menu } from '../../api/types'
import { useMenus } from '../../hooks/useMenus'
import { matchesFilter, useFilterParam } from '../../hooks/useFilterParam'
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
import { menuSourceRef } from './menuFormat'
import layout from '../../components/layout/layout.module.css'

export default function MenusList() {
  const menus = useMenus()
  const [query, setQuery] = useFilterParam()

  const filtered = (menus ?? []).filter((m) =>
    matchesFilter(query, m.name, m.namespace, m.source.oci, m.source.pantryRef?.name, m.source.version),
  )

  return (
    <div className={layout.page}>
      <PageHeader
        title="Menus"
        subtitle="Reusable templates that Orders are created from, with guardrails on what may be overridden"
        actions={<ButtonLink variant="primary" icon={<PlusIcon />} to={paths.newMenu()}>New Menu</ButtonLink>}
      />

      <Section
        title="All Menus"
        actions={<FilterInput label="Filter menus" value={query} onChange={setQuery} placeholder="Filter by name or source…" />}
        flush
      >
        {menus === null ? (
          <EmptyState text="Loading…" />
        ) : filtered.length === 0 ? (
          <EmptyState
            text={query ? 'No menus match your filter' : 'No menus yet'}
            action={!query && <ButtonLink variant="secondary" size="sm" to={paths.newMenu()}>Create your first Menu</ButtonLink>}
          />
        ) : (
          <DataTable<Menu>
            rows={filtered}
            rowKey={(m) => `${m.namespace}/${m.name}`}
            rowHref={(m) => paths.menu(m.namespace, m.name)}
            columns={[
              {
                header: 'Name',
                width: '24%',
                cell: (m) => <NameCell to={paths.menu(m.namespace, m.name)} name={m.name} secondary={m.namespace} />,
              },
              { header: 'Status', width: '110px', cell: (m) => <StatusIndicator state={m.state} /> },
              { header: 'Source', truncate: true, title: (m) => m.source.oci || menuSourceRef(m), cell: menuSourceRef },
              {
                header: 'Version',
                width: '110px',
                truncate: true,
                title: (m) => m.source.version,
                cell: (m) =>
                  m.source.version ? <span className={layout.mono}>{m.source.version}</span> : <span className={layout.muted}>—</span>,
              },
              { header: 'Created', width: '110px', cell: (m) => <RelativeTime iso={m.createdAt} /> },
            ]}
          />
        )}
      </Section>
    </div>
  )
}
