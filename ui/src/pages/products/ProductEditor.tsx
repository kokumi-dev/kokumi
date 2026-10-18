import { useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router'
import type { ProductFormData, Repository } from '../../api/types'
import { createProduct, updateProduct } from '../../api/client'
import { useProducts } from '../../hooks/useProducts'
import { useRepositories } from '../../hooks/useRegistries'
import { useUnsavedChangesGuard } from '../../hooks/useUnsavedChangesGuard'
import PageHeader from '../../components/layout/PageHeader'
import ResourceNotFound from '../../components/layout/ResourceNotFound'
import Section from '../../components/layout/Section'
import EmptyState from '../../components/layout/EmptyState'
import ProductForm from '../../components/product/ProductForm'
import { paths } from '../../routes/paths'
import layout from '../../components/layout/layout.module.css'

/** The fields of a new Product taken from a Repository: its URL, its Registry and a version. */
function fromRepository(r: Repository, version: string): Partial<ProductFormData> {
  return {
    name: r.name,
    namespace: r.namespace,
    source: { url: r.url, version },
    registryRefs: r.registryRef ? [{ name: r.registryRef }] : [],
  }
}

/**
 * Full-page create (/products/new[?repository=ns/name[&version=tag]]) and
 * edit (/products/:ns/:name/edit) screen.
 */
export default function ProductEditor() {
  const { namespace, name } = useParams()
  const [search] = useSearchParams()
  const navigate = useNavigate()
  const products = useProducts()
  const repositories = useRepositories()
  const [dirty, setDirty] = useState(false)
  const { allowNavigation, dialog } = useUnsavedChangesGuard(dirty)

  const isEdit = !!name
  const product = isEdit ? products?.find((p) => p.namespace === namespace && p.name === name) : undefined

  const repositoryKey = isEdit ? null : search.get('repository')
  const repository = repositoryKey ? repositories?.find((r) => `${r.namespace}/${r.name}` === repositoryKey) : undefined

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

  const back = product
    ? paths.product(product.namespace, product.name)
    : repository
      ? paths.repository(repository.namespace, repository.name)
      : paths.products()

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
            : repository
              ? `From Repository ${repository.name}: set how it renders and what its Orders may change.`
              : 'Define the application: its source, how it renders, and what its Orders may change.'
        }
      />
      {/* The form seeds its state once, so wait for the Repository it starts from. */}
      {repositoryKey && repositories === null ? (
        <Section><EmptyState text="Loading…" /></Section>
      ) : repositoryKey && !repository ? (
        <Section><EmptyState text={`Repository ${repositoryKey} was not found.`} /></Section>
      ) : (
        <ProductForm
          key={product ? `${product.namespace}/${product.name}` : repositoryKey ?? 'new'}
          product={product}
          initial={repository ? fromRepository(repository, search.get('version') ?? '') : undefined}
          onSubmit={handleSubmit}
          onCancel={() => navigate(back)}
          onDirtyChange={setDirty}
        />
      )}
      {dialog}
    </div>
  )
}
