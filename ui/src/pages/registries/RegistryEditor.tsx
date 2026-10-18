import { useState } from 'react'
import { useNavigate, useParams } from 'react-router'
import type { RegistryFormData } from '../../api/types'
import { createRegistry, updateRegistry } from '../../api/client'
import { useRegistries } from '../../hooks/useRegistries'
import { useUnsavedChangesGuard } from '../../hooks/useUnsavedChangesGuard'
import PageHeader from '../../components/layout/PageHeader'
import ResourceNotFound from '../../components/layout/ResourceNotFound'
import RegistryForm from '../../components/registry/RegistryForm'
import { paths } from '../../routes/paths'
import layout from '../../components/layout/layout.module.css'

/** Full-page create (/registries/new) and edit (/registries/:ns/:name/edit) screen. */
export default function RegistryEditor() {
  const { namespace, name } = useParams()
  const navigate = useNavigate()
  const registries = useRegistries()
  const [dirty, setDirty] = useState(false)
  const { allowNavigation, dialog } = useUnsavedChangesGuard(dirty)

  const isEdit = !!name
  const registry = isEdit ? registries?.find((r) => r.namespace === namespace && r.name === name) : undefined

  if (isEdit && !registry) {
    return (
      <ResourceNotFound kind="Registry" listPath={paths.registries()} listLabel="Registries" namespace={namespace} name={name} loading={registries === null} />
    )
  }

  async function handleSubmit(data: RegistryFormData) {
    if (registry) await updateRegistry(registry, data)
    else await createRegistry(data)
    allowNavigation()
    navigate(paths.registry(data.namespace, data.name))
  }

  const back = registry ? paths.registry(registry.namespace, registry.name) : paths.registries()

  return (
    <div className={layout.page}>
      <PageHeader
        breadcrumbs={
          registry
            ? [{ label: 'Registries', to: paths.registries() }, { label: registry.namespace }, { label: registry.name, to: back }]
            : [{ label: 'Registries', to: paths.registries() }]
        }
        title={registry ? 'Edit Registry' : 'New Registry'}
        subtitle="Products and Orders that list this Registry connect through it to the locations below its URL."
      />
      <RegistryForm
        key={registry ? `${registry.namespace}/${registry.name}` : 'new'}
        registry={registry}
        onSubmit={handleSubmit}
        onCancel={() => navigate(back)}
        onDirtyChange={setDirty}
      />
      {dialog}
    </div>
  )
}
