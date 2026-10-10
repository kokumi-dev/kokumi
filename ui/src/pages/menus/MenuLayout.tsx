import { useState } from 'react'
import { Outlet, useNavigate, useParams } from 'react-router'
import { deleteMenu } from '../../api/client'
import { useMenus } from '../../hooks/useMenus'
import { useOrders } from '../../hooks/useOrders'
import PageHeader from '../../components/layout/PageHeader'
import ResourceNotFound from '../../components/layout/ResourceNotFound'
import Badge from '../../components/shared/Badge'
import ConfirmDialog from '../../components/shared/ConfirmDialog'
import { Button, ButtonLink, EditIcon, PlusIcon, TabNav, TrashIcon } from '../../components/ui'
import { paths } from '../../routes/paths'
import { menuSourceLabel } from './menuFormat'
import layout from '../../components/layout/layout.module.css'

export default function MenuLayout() {
  const { namespace = '', name = '' } = useParams()
  const navigate = useNavigate()
  const menus = useMenus()
  const orders = useOrders()
  const [confirmDelete, setConfirmDelete] = useState(false)

  const menu = menus?.find((m) => m.namespace === namespace && m.name === name)
  if (!menu) {
    return (
      <ResourceNotFound kind="Menu" listPath={paths.menus()} listLabel="Menus" namespace={namespace} name={name} loading={menus === null} />
    )
  }

  const orderCount = orders?.filter((o) => o.namespace === menu.namespace && o.menuRef?.name === menu.name).length

  return (
    <div className={layout.page}>
      <PageHeader
        breadcrumbs={[{ label: 'Menus', to: paths.menus() }, { label: menu.namespace }]}
        title={menu.name}
        badge={<Badge state={menu.state ?? ''} />}
        subtitle={<span className={layout.mono}>{menuSourceLabel(menu)}</span>}
        actions={
          <>
            <ButtonLink variant="secondary" icon={<EditIcon />} to={paths.menu(menu.namespace, menu.name, 'edit')}>Edit</ButtonLink>
            <Button variant="danger" icon={<TrashIcon />} onClick={() => setConfirmDelete(true)}>Delete</Button>
            <ButtonLink variant="primary" icon={<PlusIcon />} to={paths.newOrder(menu)}>Use this menu</ButtonLink>
          </>
        }
      />

      <TabNav
        tabs={[
          { to: paths.menu(menu.namespace, menu.name), label: 'Overview', end: true },
          { to: paths.menu(menu.namespace, menu.name, 'orders'), label: 'Orders', count: orderCount },
        ]}
      />

      <Outlet context={{ menu }} />

      {confirmDelete && (
        <ConfirmDialog
          title={`Delete Menu ${menu.name}`}
          confirmLabel="Delete Menu"
          variant="danger"
          onCancel={() => setConfirmDelete(false)}
          onConfirm={async () => {
            await deleteMenu(menu.namespace, menu.name)
            navigate(paths.menus())
          }}
        >
          This deletes <strong>{menu.namespace}/{menu.name}</strong>.
          {orderCount ? ` ${orderCount} Order(s) still reference it.` : ''}
        </ConfirmDialog>
      )}
    </div>
  )
}
