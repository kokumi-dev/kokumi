import { useState } from 'react'
import { useNavigate, useParams } from 'react-router'
import type { PantryFormData } from '../../api/types'
import { createPantry, updatePantry } from '../../api/client'
import { usePantries } from '../../hooks/usePantries'
import { useUnsavedChangesGuard } from '../../hooks/useUnsavedChangesGuard'
import PageHeader from '../../components/layout/PageHeader'
import ResourceNotFound from '../../components/layout/ResourceNotFound'
import PantryForm from '../../components/pantry/PantryForm'
import { paths } from '../../routes/paths'
import layout from '../../components/layout/layout.module.css'

/** Full-page create (/pantries/new) and edit (/pantries/:ns/:name/edit) screen. */
export default function PantryEditor() {
  const { namespace, name } = useParams()
  const navigate = useNavigate()
  const pantries = usePantries()
  const [dirty, setDirty] = useState(false)
  const { allowNavigation, dialog } = useUnsavedChangesGuard(dirty)

  const isEdit = !!name
  const pantry = isEdit ? pantries?.find((p) => p.namespace === namespace && p.name === name) : undefined

  if (isEdit && !pantry) {
    return (
      <ResourceNotFound kind="Pantry" listPath={paths.pantries()} listLabel="Pantries" namespace={namespace} name={name} loading={pantries === null} />
    )
  }

  async function handleSubmit(data: PantryFormData) {
    if (pantry) await updatePantry(pantry.namespace, pantry.name, data)
    else await createPantry(data)
    allowNavigation()
    navigate(paths.pantry(data.namespace, data.name))
  }

  const back = pantry ? paths.pantry(pantry.namespace, pantry.name) : paths.pantries()

  return (
    <div className={layout.page}>
      <PageHeader
        breadcrumbs={
          pantry
            ? [{ label: 'Pantries', to: paths.pantries() }, { label: pantry.namespace }, { label: pantry.name, to: back }]
            : [{ label: 'Pantries', to: paths.pantries() }]
        }
        title={pantry ? 'Edit Pantry' : 'New Pantry'}
        subtitle="Connect an OCI registry so Menus and Orders can pull from and push to it."
      />
      <PantryForm
        key={pantry ? `${pantry.namespace}/${pantry.name}` : 'new'}
        pantry={pantry}
        onSubmit={handleSubmit}
        onCancel={() => navigate(back)}
        onDirtyChange={setDirty}
      />
      {dialog}
    </div>
  )
}
