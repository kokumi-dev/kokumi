import { useState } from 'react'
import { Link, Outlet, useNavigate, useParams } from 'react-router'
import { deleteProduct } from '../../api/client'
import { useProducts } from '../../hooks/useProducts'
import { useOrders } from '../../hooks/useOrders'
import { useDeniedReason } from '../../hooks/useCapability'
import PageHeader from '../../components/layout/PageHeader'
import ResourceNotFound from '../../components/layout/ResourceNotFound'
import Badge from '../../components/shared/Badge'
import ConfirmDialog from '../../components/shared/ConfirmDialog'
import RelativeTime from '../../components/shared/RelativeTime'
import { Button, ButtonLink, EditIcon, PlusIcon, TabNav, TrashIcon } from '../../components/ui'
import { paths } from '../../routes/paths'
import { plural } from '../../utils/format'
import { productDeletingReason, productSourceLabel } from '../../utils/products'
import layout from '../../components/layout/layout.module.css'

export default function ProductLayout() {
  const { namespace = '', name = '' } = useParams()
  const navigate = useNavigate()
  const products = useProducts()
  const orders = useOrders()
  const [confirmDelete, setConfirmDelete] = useState(false)
  const editDenied = useDeniedReason('products:update')
  const deleteDenied = useDeniedReason('products:delete')
  const orderDenied = useDeniedReason('orders:create')

  const product = products?.find((p) => p.namespace === namespace && p.name === name)
  if (!product) {
    return (
      <ResourceNotFound kind="Product" listPath={paths.products()} listLabel="Products" namespace={namespace} name={name} loading={products === null} />
    )
  }

  const orderCount = orders?.filter((o) => o.namespace === product.namespace && o.productRef.name === product.name).length
  // A deleted Product waits for its Orders; it takes no changes or new Orders meanwhile.
  const deleting = productDeletingReason(product)
  const ready = product.conditions?.find((c) => c.type === 'Ready')
  const blockedBy = ready?.reason === 'InUse' ? ready.message : undefined

  return (
    <div className={layout.page}>
      <PageHeader
        breadcrumbs={[{ label: 'Products', to: paths.products() }, { label: product.namespace }]}
        title={product.name}
        badge={<Badge state={product.state ?? ''} />}
        subtitle={<span className={layout.mono}>{productSourceLabel(product)}</span>}
        actions={
          <>
            <ButtonLink variant="secondary" icon={<EditIcon />} to={paths.product(product.namespace, product.name, 'edit')} disabledReason={deleting ?? editDenied}>Edit</ButtonLink>
            <Button variant="danger" icon={<TrashIcon />} onClick={() => setConfirmDelete(true)} disabledReason={deleting ?? deleteDenied}>Delete</Button>
            <ButtonLink variant="primary" icon={<PlusIcon />} to={paths.newOrder(product)} disabledReason={deleting ?? orderDenied}>New Order</ButtonLink>
          </>
        }
      />

      {deleting && (
        <div className={layout.notice} role="status">
          <strong>Deletion requested <RelativeTime iso={product.deletionTimestamp} /></strong>
          <span>{blockedBy ?? 'The Product is removed once its Orders are deleted'}</span>
          <Link className={layout.inlineLink} to={paths.product(product.namespace, product.name, 'orders')}>Show Orders</Link>
        </div>
      )}

      <TabNav
        tabs={[
          { to: paths.product(product.namespace, product.name), label: 'Overview', end: true },
          { to: paths.product(product.namespace, product.name, 'orders'), label: 'Orders', count: orderCount },
          { to: paths.product(product.namespace, product.name, 'preview'), label: 'Preview' },
        ]}
      />

      <Outlet context={{ product }} />

      {confirmDelete && (
        <ConfirmDialog
          title={`Delete Product ${product.name}`}
          confirmLabel="Delete Product"
          variant="danger"
          onCancel={() => setConfirmDelete(false)}
          onConfirm={async () => {
            await deleteProduct(product.namespace, product.name)
            navigate(paths.products())
          }}
        >
          This deletes <strong>{product.namespace}/{product.name}</strong>.
          {orderCount
            ? ` It still has ${plural(orderCount, 'Order')}; the Product stays until they are deleted.`
            : ''}
        </ConfirmDialog>
      )}
    </div>
  )
}
