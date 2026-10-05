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
import { BtnLink } from '../../components/shared/Btn'
import { paths } from '../../routes/paths'
import { menuSourceLabel, menuSourceShort } from './menuFormat'
import layout from '../../components/layout/layout.module.css'

const overridesLabel = (m: Menu) => `Values: ${m.overrides.values.policy}, Patches: ${m.overrides.patches.policy}`

export default function MenusList() {
  const menus = useMenus()
  const [query, setQuery] = useFilterParam()

  const filtered = (menus ?? []).filter((m) => matchesFilter(query, m.name, m.namespace, m.source.oci, m.source.pantryRef?.name))

  return (
    <div className={layout.page}>
      <PageHeader
        title="Menus"
        subtitle="Reusable templates that Orders are created from, with guardrails on what may be overridden"
        actions={<BtnLink variant="primary" to={paths.newMenu()}>New Menu</BtnLink>}
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
            action={!query && <BtnLink variant="secondary" size="sm" to={paths.newMenu()}>Create your first Menu</BtnLink>}
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
              { header: 'Source', truncate: true, title: menuSourceLabel, cell: menuSourceShort },
              { header: 'Overrides', width: '26%', truncate: true, title: overridesLabel, cell: overridesLabel },
              { header: 'Created', width: '110px', cell: (m) => <RelativeTime iso={m.createdAt} /> },
            ]}
          />
        )}
      </Section>
    </div>
  )
}
