import { useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router'
import type { OrderFormData } from '../../api/types'
import { createOrder, updateOrder } from '../../api/client'
import { useOrders } from '../../hooks/useOrders'
import { useMenus } from '../../hooks/useMenus'
import { useUnsavedChangesGuard } from '../../hooks/useUnsavedChangesGuard'
import PageHeader from '../../components/layout/PageHeader'
import ResourceNotFound from '../../components/layout/ResourceNotFound'
import Section from '../../components/layout/Section'
import EmptyState from '../../components/layout/EmptyState'
import OrderForm from '../../components/order/OrderForm'
import { paths } from '../../routes/paths'
import layout from '../../components/layout/layout.module.css'

/** Full-page create (/orders/new) and edit (/orders/:ns/:name/edit) screen. */
export default function OrderEditor() {
  const { namespace, name } = useParams()
  const [search] = useSearchParams()
  const navigate = useNavigate()
  const orders = useOrders()
  const menus = useMenus()
  const [dirty, setDirty] = useState(false)
  const { allowNavigation, dialog } = useUnsavedChangesGuard(dirty)

  const isEdit = !!name
  const order = isEdit ? orders?.find((o) => o.namespace === namespace && o.name === name) : undefined

  const menuKey = search.get('menu')
  const presetMenu = menuKey ? menus?.find((m) => `${m.namespace}/${m.name}` === menuKey) : undefined
  const orderMenu = order?.menuRef
    ? menus?.find((m) => m.namespace === order.namespace && m.name === order.menuRef?.name)
    : undefined

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

  // The form seeds its state once, so wait for the data it depends on.
  const waitingForMenus = menus === null && (!!menuKey || !!order?.menuRef)

  async function handleSubmit(data: OrderFormData, commitMessage: string) {
    if (order) {
      await updateOrder(order.namespace, order.name, data, commitMessage)
    } else {
      await createOrder(data, commitMessage)
    }
    allowNavigation()
    navigate(paths.order(data.namespace, data.name))
  }

  const back = order ? paths.order(order.namespace, order.name) : presetMenu ? paths.menu(presetMenu.namespace, presetMenu.name) : paths.orders()

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
            : presetMenu
              ? `Order from Menu ${presetMenu.name}. Source and renderer come from the Menu.`
              : 'Pick a Menu or an OCI source, configure it, and preview the result before creating.'
        }
      />

      {waitingForMenus ? (
        <Section><EmptyState text="Loading…" /></Section>
      ) : menuKey && !presetMenu ? (
        <Section><EmptyState text={`Menu ${menuKey} was not found.`} /></Section>
      ) : (
        <OrderForm
          key={order ? `${order.namespace}/${order.name}` : menuKey ?? 'new'}
          order={order}
          menuRef={presetMenu ? { name: presetMenu.name } : undefined}
          menu={orderMenu ?? presetMenu}
          menus={menus ?? undefined}
          onSubmit={handleSubmit}
          onCancel={() => navigate(back)}
          onDirtyChange={setDirty}
        />
      )}

      {dialog}
    </div>
  )
}
