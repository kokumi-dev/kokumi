import { Link } from 'react-router'
import type { Order } from '../../api/types'
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
import { stateTone } from '../../utils/state'
import layout from '../../components/layout/layout.module.css'

export default function OrdersList() {
  const orders = useOrders()
  const [query, setQuery] = useFilterParam()
  const createDenied = useDeniedReason('orders:create')

  const filtered = (orders ?? []).filter((o) =>
    matchesFilter(query, o.name, o.namespace, o.productRef.name),
  )

  return (
    <div className={layout.page}>
      <PageHeader
        title="Orders"
        subtitle="Variants of Products: what differs per variant, and where and how it gets promoted"
        actions={<ButtonLink variant="primary" icon={<PlusIcon />} to={paths.newOrder()} disabledReason={createDenied}>New Order</ButtonLink>}
      />

      <Section
        title="All Orders"
        actions={<FilterInput label="Filter orders" value={query} onChange={setQuery} placeholder="Filter by name, namespace or Product…" />}
        flush
      >
        {orders === null ? (
          <EmptyState text="Loading…" />
        ) : filtered.length === 0 ? (
          <EmptyState
            text={query ? 'No orders match your filter' : 'No orders yet'}
            action={!query && <ButtonLink variant="secondary" size="sm" to={paths.newOrder()} disabledReason={createDenied}>Create your first Order</ButtonLink>}
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
              { header: 'Status', width: '130px', cell: (o) => <StatusIndicator state={o.state} tone={stateTone(o)} /> },
              {
                header: 'Product',
                truncate: true,
                title: (o) => o.productRef.name,
                cell: (o) => (
                  <Link className={layout.refLink} to={paths.product(o.namespace, o.productRef.name)}>
                    {o.productRef.name}
                  </Link>
                ),
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
