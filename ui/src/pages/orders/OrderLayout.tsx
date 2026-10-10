import { useMemo, useState } from 'react'
import { Outlet, useNavigate, useParams } from 'react-router'
import { deleteOrder } from '../../api/client'
import { useOrders } from '../../hooks/useOrders'
import { useMenus } from '../../hooks/useMenus'
import { usePreparations } from '../../hooks/usePreparations'
import { useServings } from '../../hooks/useServings'
import PageHeader from '../../components/layout/PageHeader'
import ResourceNotFound from '../../components/layout/ResourceNotFound'
import Badge from '../../components/shared/Badge'
import { Button, ButtonLink, EditIcon, TabNav, TrashIcon } from '../../components/ui'
import ConfirmDialog from '../../components/shared/ConfirmDialog'
import { paths } from '../../routes/paths'
import { preparationsOf, sortByChain } from '../../utils/preparations'
import { orderSourceLabel } from './orderFormat'
import type { OrderContext } from './orderContext'
import layout from '../../components/layout/layout.module.css'

export default function OrderLayout() {
  const { namespace = '', name = '' } = useParams()
  const navigate = useNavigate()
  const orders = useOrders()
  const menus = useMenus()
  const allPreps = usePreparations()
  const allServings = useServings()
  const [confirmDelete, setConfirmDelete] = useState(false)

  const order = orders?.find((o) => o.namespace === namespace && o.name === name)
  const menu = order?.menuRef
    ? menus?.find((m) => m.namespace === order.namespace && m.name === order.menuRef?.name)
    : undefined

  const preparations = useMemo(() => {
    const list = preparationsOf(allPreps, namespace, name)
    return list === null ? null : sortByChain(list)
  }, [allPreps, namespace, name])

  const servings = useMemo(
    () => allServings?.filter((s) => s.namespace === namespace && s.order === name) ?? null,
    [allServings, namespace, name],
  )

  if (!order) {
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

  // When a menuRef is set but the menu has not loaded, deny edits (fail closed).
  const editsAllowed = order.menuRef ? !!menu && menu.overrides.patches.policy !== 'None' : true

  const ctx: OrderContext = { order, menu, editsAllowed, preparations, servings }

  return (
    <div className={layout.page}>
      <PageHeader
        breadcrumbs={[{ label: 'Orders', to: paths.orders() }, { label: order.namespace }]}
        title={order.name}
        badge={<Badge state={order.state} />}
        subtitle={<span className={layout.mono}>{orderSourceLabel(order)}</span>}
        actions={
          <>
            <ButtonLink variant="secondary" icon={<EditIcon />} to={paths.order(order.namespace, order.name, 'edit')}>Edit</ButtonLink>
            <Button variant="danger" icon={<TrashIcon />} onClick={() => setConfirmDelete(true)}>Delete</Button>
          </>
        }
      />

      <TabNav
        tabs={[
          { to: paths.order(order.namespace, order.name), label: 'Overview', end: true },
          { to: paths.order(order.namespace, order.name, 'preparations'), label: 'Preparations', count: preparations?.length },
          { to: paths.order(order.namespace, order.name, 'servings'), label: 'Servings' },
          { to: paths.order(order.namespace, order.name, 'files'), label: 'Files' },
        ]}
      />

      <Outlet context={ctx} />

      {confirmDelete && (
        <ConfirmDialog
          title={`Delete Order ${order.name}`}
          confirmLabel="Delete Order"
          variant="danger"
          onCancel={() => setConfirmDelete(false)}
          onConfirm={async () => {
            await deleteOrder(order.namespace, order.name)
            navigate(paths.orders())
          }}
        >
          This deletes <strong>{order.namespace}/{order.name}</strong> together with its Preparations and Serving.
        </ConfirmDialog>
      )}
    </div>
  )
}
