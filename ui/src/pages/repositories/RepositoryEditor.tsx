import { useState } from 'react'
import { useNavigate, useParams } from 'react-router'
import type { RepositoryFormData } from '../../api/types'
import { createRepository, updateRepository } from '../../api/client'
import { useRepositories } from '../../hooks/useRegistries'
import { useUnsavedChangesGuard } from '../../hooks/useUnsavedChangesGuard'
import PageHeader from '../../components/layout/PageHeader'
import ResourceNotFound from '../../components/layout/ResourceNotFound'
import RepositoryForm from '../../components/repository/RepositoryForm'
import { paths } from '../../routes/paths'
import layout from '../../components/layout/layout.module.css'

/** Full-page create (/repositories/new) and edit (/repositories/:ns/:name/edit) screen. */
export default function RepositoryEditor() {
  const { namespace, name } = useParams()
  const navigate = useNavigate()
  const repositories = useRepositories()
  const [dirty, setDirty] = useState(false)
  const { allowNavigation, dialog } = useUnsavedChangesGuard(dirty)

  const isEdit = !!name
  const repository = isEdit ? repositories?.find((r) => r.namespace === namespace && r.name === name) : undefined

  if (isEdit && !repository) {
    return (
      <ResourceNotFound kind="Repository" listPath={paths.repositories()} listLabel="Repositories" namespace={namespace} name={name} loading={repositories === null} />
    )
  }

  async function handleSubmit(data: RepositoryFormData) {
    if (repository) await updateRepository(repository.namespace, repository.name, data)
    else await createRepository(data)
    allowNavigation()
    navigate(paths.repository(data.namespace, data.name))
  }

  const back = repository ? paths.repository(repository.namespace, repository.name) : paths.repositories()

  return (
    <div className={layout.page}>
      <PageHeader
        breadcrumbs={
          repository
            ? [{ label: 'Repositories', to: paths.repositories() }, { label: repository.namespace }, { label: repository.name, to: back }]
            : [{ label: 'Repositories', to: paths.repositories() }]
        }
        title={repository ? 'Edit Repository' : 'New Repository'}
        subtitle="Add an OCI repository to the catalog to browse its tags and create Products from it."
      />
      <RepositoryForm
        key={repository ? `${repository.namespace}/${repository.name}` : 'new'}
        repository={repository}
        onSubmit={handleSubmit}
        onCancel={() => navigate(back)}
        onDirtyChange={setDirty}
      />
      {dialog}
    </div>
  )
}
