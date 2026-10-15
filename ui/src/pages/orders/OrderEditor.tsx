import { useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router'
import type { OrderFormData } from '../../api/types'
import { createOrder, updateOrder } from '../../api/client'
import { useOrders } from '../../hooks/useOrders'
import { useProducts } from '../../hooks/useProducts'
import { useUnsavedChangesGuard } from '../../hooks/useUnsavedChangesGuard'
import PageHeader from '../../components/layout/PageHeader'
import ResourceNotFound from '../../components/layout/ResourceNotFound'
import Section from '../../components/layout/Section'
import EmptyState from '../../components/layout/EmptyState'
import OrderForm from '../../components/order/OrderForm'
import { paths } from '../../routes/paths'
import layout from '../../components/layout/layout.module.css'

/** Full-page create (/orders/new[?product=ns/name]) and edit (/orders/:ns/:name/edit) screen. */
export default function OrderEditor() {
  const { namespace, name } = useParams()
  const [search] = useSearchParams()
  const navigate = useNavigate()
  const orders = useOrders()
  const products = useProducts()
  const [dirty, setDirty] = useState(false)
  const { allowNavigation, dialog } = useUnsavedChangesGuard(dirty)

  const isEdit = !!name
  const order = isEdit ? orders?.find((o) => o.namespace === namespace && o.name === name) : undefined

  const productKey = search.get('product')
  const presetProduct = productKey ? products?.find((p) => `${p.namespace}/${p.name}` === productKey) : undefined

  if (isEdit && !order) {
    return (
      <ResourceNotFound
        kind="Order"
        listPath={paths.orders()}
        listLabel="Orders"
        namespace={namespace}
        name={name}
        loading={orders === null}
      />
    )
  }

  async function handleSubmit(data: OrderFormData, commitMessage: string) {
    if (order) {
      await updateOrder(order.namespace, order.name, data, commitMessage)
    } else {
      await createOrder(data, commitMessage)
    }
    allowNavigation()
    navigate(paths.order(data.namespace, data.name))
  }

  const back = order
    ? paths.order(order.namespace, order.name)
    : presetProduct
      ? paths.product(presetProduct.namespace, presetProduct.name)
      : paths.orders()

  return (
    <div className={layout.page}>
      <PageHeader
        breadcrumbs={
          order
            ? [{ label: 'Orders', to: paths.orders() }, { label: order.namespace }, { label: order.name, to: back }]
            : [{ label: 'Orders', to: paths.orders() }]
        }
        title={order ? 'Edit Order' : 'New Order'}
        subtitle={
          order
            ? 'Changes are committed to the Order and render a new Preparation.'
            : presetProduct
              ? `A variant of Product ${presetProduct.name}: set what differs and preview the result before creating.`
              : 'Pick the Product this Order is a variant of, set what differs, and preview the result before creating.'
        }
      />

      {/* The form seeds its state once, so wait for the Products it depends on. */}
      {products === null ? (
        <Section><EmptyState text="Loading…" /></Section>
      ) : productKey && !presetProduct ? (
        <Section><EmptyState text={`Product ${productKey} was not found.`} /></Section>
      ) : (
        <OrderForm
          key={order ? `${order.namespace}/${order.name}` : productKey ?? 'new'}
          order={order}
          initialProduct={presetProduct}
          products={products}
          onSubmit={handleSubmit}
          onCancel={() => navigate(back)}
          onDirtyChange={setDirty}
        />
      )}

      {dialog}
    </div>
  )
}
