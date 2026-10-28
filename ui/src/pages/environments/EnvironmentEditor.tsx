import { useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router'
import type { EnvironmentFormData } from '../../api/types'
import { createEnvironment, updateEnvironment } from '../../api/client'
import { useEnvironments } from '../../hooks/useEnvironments'
import { useUnsavedChangesGuard } from '../../hooks/useUnsavedChangesGuard'
import PageHeader from '../../components/layout/PageHeader'
import ResourceNotFound from '../../components/layout/ResourceNotFound'
import EnvironmentForm from '../../components/environment/EnvironmentForm'
import { paths } from '../../routes/paths'
import layout from '../../components/layout/layout.module.css'

/** Full-page create (/environments/new[?namespace=ns]) and edit (/environments/:ns/:name/edit) screen. */
export default function EnvironmentEditor() {
  const { namespace, name } = useParams()
  const [search] = useSearchParams()
  const navigate = useNavigate()
  const environments = useEnvironments()
  const [dirty, setDirty] = useState(false)
  const { allowNavigation, dialog } = useUnsavedChangesGuard(dirty)

  const isEdit = !!name
  const environment = isEdit ? environments?.find((e) => e.namespace === namespace && e.name === name) : undefined

  if (isEdit && !environment) {
    return (
      <ResourceNotFound
        kind="Environment"
        listPath={paths.environments()}
        listLabel="Environments"
        namespace={namespace}
        name={name}
        loading={environments === null}
      />
    )
  }

  async function handleSubmit(data: EnvironmentFormData) {
    if (environment) await updateEnvironment(environment.namespace, environment.name, data)
    else await createEnvironment(data)
    allowNavigation()
    navigate(paths.environment(data.namespace, data.name))
  }

  const back = environment ? paths.environment(environment.namespace, environment.name) : paths.environments()

  return (
    <div className={layout.page}>
      <PageHeader
        breadcrumbs={
          environment
            ? [{ label: 'Environments', to: paths.environments() }, { label: environment.namespace }, { label: environment.name, to: back }]
            : [{ label: 'Environments', to: paths.environments() }]
        }
        title={environment ? 'Edit Environment' : 'New Environment'}
        subtitle="Orders of its namespace that name this Environment deploy to the Clusters it selects, through one Argo CD Application each."
      />
      <EnvironmentForm
        key={environment ? `${environment.namespace}/${environment.name}` : 'new'}
        environment={environment}
        initialNamespace={search.get('namespace') || undefined}
        onSubmit={handleSubmit}
        onCancel={() => navigate(back)}
        onDirtyChange={setDirty}
      />
      {dialog}
    </div>
  )
}
