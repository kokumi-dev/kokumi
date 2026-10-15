import { useState } from 'react'
import { useNavigate, useParams } from 'react-router'
import type { ProductFormData } from '../../api/types'
import { createProduct, updateProduct } from '../../api/client'
import { useProducts } from '../../hooks/useProducts'
import { useUnsavedChangesGuard } from '../../hooks/useUnsavedChangesGuard'
import PageHeader from '../../components/layout/PageHeader'
import ResourceNotFound from '../../components/layout/ResourceNotFound'
import ProductForm from '../../components/product/ProductForm'
import { paths } from '../../routes/paths'
import layout from '../../components/layout/layout.module.css'

/** Full-page create (/products/new) and edit (/products/:ns/:name/edit) screen. */
export default function ProductEditor() {
  const { namespace, name } = useParams()
  const navigate = useNavigate()
  const products = useProducts()
  const [dirty, setDirty] = useState(false)
  const { allowNavigation, dialog } = useUnsavedChangesGuard(dirty)

  const isEdit = !!name
  const product = isEdit ? products?.find((p) => p.namespace === namespace && p.name === name) : undefined

  if (isEdit && !product) {
    return (
      <ResourceNotFound kind="Product" listPath={paths.products()} listLabel="Products" namespace={namespace} name={name} loading={products === null} />
    )
  }

  async function handleSubmit(data: ProductFormData) {
    if (product) await updateProduct(product.namespace, product.name, data)
    else await createProduct(data)
    allowNavigation()
    navigate(paths.product(data.namespace, data.name))
  }

  const back = product ? paths.product(product.namespace, product.name) : paths.products()

  return (
    <div className={layout.page}>
      <PageHeader
        breadcrumbs={
          product
            ? [{ label: 'Products', to: paths.products() }, { label: product.namespace }, { label: product.name, to: back }]
            : [{ label: 'Products', to: paths.products() }]
        }
        title={product ? 'Edit Product' : 'New Product'}
        subtitle={
          product
            ? 'Changes apply to every Order of this Product and render new Preparations.'
            : 'Define the application: its source, how it renders, and what its Orders may change.'
        }
      />
      <ProductForm
        key={product ? `${product.namespace}/${product.name}` : 'new'}
        product={product}
        onSubmit={handleSubmit}
        onCancel={() => navigate(back)}
        onDirtyChange={setDirty}
      />
      {dialog}
    </div>
  )
}
