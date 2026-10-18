import { useMemo, useState } from 'react'
import type { Product } from '../../api/types'
import { useProducts } from '../../hooks/useProducts'
import { useOrders } from '../../hooks/useOrders'
import { usePreparations } from '../../hooks/usePreparations'
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
import { ButtonLink, Counter, PlusIcon } from '../../components/ui'
import { paths } from '../../routes/paths'
import { ordersByProduct, productDeletingReason, productKey, productSourceRef } from '../../utils/products'
import layout from '../../components/layout/layout.module.css'
import { orderInsights } from './orderInsights'
import ProductOrderTree from './ProductOrderTree'

export default function ProductsList() {
  const products = useProducts()
  const orders = useOrders()
  const preparations = usePreparations()
  const orderDenied = useDeniedReason('orders:create')
  const [query, setQuery] = useFilterParam()
  const createDenied = useDeniedReason('products:create')
  // Rows the user opened or closed; the others follow the filter.
  const [toggled, setToggled] = useState<ReadonlyMap<string, boolean>>(() => new Map())

  const groups = useMemo(() => ordersByProduct(orders), [orders])
  const ordersOf = (p: Product) => groups.get(productKey(p.namespace, p.name)) ?? []

  // Products that match only through one of their Orders open by themselves.
  const matchedViaOrder = new Set<string>()
  const filtered = (products ?? []).filter((p) => {
    if (matchesFilter(query, p.name, p.namespace, p.source.url, p.source.version)) return true
    if (!ordersOf(p).some((o) => matchesFilter(query, o.name))) return false
    matchedViaOrder.add(productKey(p.namespace, p.name))
    return true
  })

  const isOpen = (p: Product) => {
    const key = productKey(p.namespace, p.name)
    return toggled.get(key) ?? matchedViaOrder.has(key)
  }

  return (
    <div className={layout.page}>
      <PageHeader
        title="Products"
        subtitle="Applications and their base configuration. Each Order is a variant of a Product."
        actions={<ButtonLink variant="primary" icon={<PlusIcon />} to={paths.newProduct()} disabledReason={createDenied}>New Product</ButtonLink>}
      />

      <Section
        title="All Products"
        actions={<FilterInput label="Filter products" value={query} onChange={setQuery} placeholder="Filter by name, source or Order…" />}
        flush
      >
        {products === null ? (
          <EmptyState text="Loading…" />
        ) : filtered.length === 0 ? (
          <EmptyState
            text={query ? 'No products match your filter' : 'No products yet'}
            action={!query && <ButtonLink variant="secondary" size="sm" to={paths.newProduct()} disabledReason={createDenied}>Create your first Product</ButtonLink>}
          />
        ) : (
          <DataTable<Product>
            rows={filtered}
            rowKey={(p) => productKey(p.namespace, p.name)}
            rowHref={(p) => paths.product(p.namespace, p.name)}
            expand={{
              isOpen,
              onToggle: (p) => setToggled((prev) => new Map(prev).set(productKey(p.namespace, p.name), !isOpen(p))),
              toggleLabel: (p, open) => `${open ? 'Hide' : 'Show'} Orders of ${p.name}`,
              render: (p) => (
                <ProductOrderTree
                  product={p}
                  insights={orders === null ? null : orderInsights(ordersOf(p), preparations)}
                  orderDenied={productDeletingReason(p) ?? orderDenied}
                />
              ),
              connected: true,
            }}
            columns={[
              {
                header: 'Name',
                width: '22%',
                cell: (p) => <NameCell to={paths.product(p.namespace, p.name)} name={p.name} secondary={p.namespace} />,
              },
              { header: 'Status', width: '110px', cell: (p) => <StatusIndicator state={p.state} /> },
              { header: 'Source', truncate: true, title: (p) => p.source.url || productSourceRef(p), cell: productSourceRef },
              {
                header: 'Version',
                width: '110px',
                truncate: true,
                title: (p) => p.source.version,
                cell: (p) =>
                  p.source.version ? <span className={layout.mono}>{p.source.version}</span> : <span className={layout.muted}>—</span>,
              },
              {
                header: 'Orders',
                width: '100px',
                cell: (p) => {
                  if (orders === null) return <span className={layout.muted}>—</span>
                  const count = ordersOf(p).length
                  return count > 0 ? <Counter count={count} /> : <span className={layout.muted}>0</span>
                },
              },
              { header: 'Created', width: '110px', cell: (p) => <RelativeTime iso={p.createdAt} /> },
            ]}
          />
        )}
      </Section>
    </div>
  )
}
