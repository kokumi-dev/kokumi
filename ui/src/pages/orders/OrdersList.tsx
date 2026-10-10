import { Link } from 'react-router'
import type { Order } from '../../api/types'
import { useOrders } from '../../hooks/useOrders'
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
import { orderSourceRef } from './orderFormat'
import layout from '../../components/layout/layout.module.css'

export default function OrdersList() {
  const orders = useOrders()
  const [query, setQuery] = useFilterParam()

  const filtered = (orders ?? []).filter((o) =>
    matchesFilter(query, o.name, o.namespace, o.menuRef?.name, o.source?.oci, o.source?.pantryRef?.name, o.source?.version),
  )

  return (
    <div className={layout.page}>
      <PageHeader
        title="Orders"
        subtitle="What to render, how to configure it, and where it gets promoted"
        actions={<BtnLink variant="primary" to={paths.newOrder()}>New Order</BtnLink>}
      />

      <Section
        title="All Orders"
        actions={<FilterInput label="Filter orders" value={query} onChange={setQuery} placeholder="Filter by name, namespace or source…" />}
        flush
      >
        {orders === null ? (
          <EmptyState text="Loading…" />
        ) : filtered.length === 0 ? (
          <EmptyState
            text={query ? 'No orders match your filter' : 'No orders yet'}
            action={!query && <BtnLink variant="secondary" size="sm" to={paths.newOrder()}>Create your first Order</BtnLink>}
          />
        ) : (
          <DataTable<Order>
            rows={filtered}
            rowKey={(o) => `${o.namespace}/${o.name}`}
            rowHref={(o) => paths.order(o.namespace, o.name)}
            columns={[
              {
                header: 'Name',
                width: '20%',
                cell: (o) => <NameCell to={paths.order(o.namespace, o.name)} name={o.name} secondary={o.namespace} />,
              },
              { header: 'Status', width: '110px', cell: (o) => <StatusIndicator state={o.state} /> },
              { header: 'Source', truncate: true, title: (o) => o.source?.oci ?? orderSourceRef(o), cell: orderSourceRef },
              {
                header: 'Version',
                width: '110px',
                truncate: true,
                title: (o) => o.source?.version,
                cell: (o) =>
                  o.source?.version ? <span className={layout.mono}>{o.source.version}</span> : <span className={layout.muted}>—</span>,
              },
              {
                header: 'Active',
                width: '16%',
                truncate: true,
                title: (o) => o.activePreparation,
                cell: (o) =>
                  o.activePreparation ? (
                    <Link className={layout.refLink} to={paths.preparation(o.namespace, o.activePreparation)}>
                      {o.activePreparation}
                    </Link>
                  ) : (
                    <span className={layout.muted}>—</span>
                  ),
              },
              { header: 'Created', width: '110px', cell: (o) => <RelativeTime iso={o.createdAt} /> },
            ]}
          />
        )}
      </Section>
    </div>
  )
}
