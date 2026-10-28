import { useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router'
import type { ClusterFormData } from '../../api/types'
import { createCluster, updateCluster } from '../../api/client'
import { useClusters } from '../../hooks/useEnvironments'
import { useUnsavedChangesGuard } from '../../hooks/useUnsavedChangesGuard'
import PageHeader from '../../components/layout/PageHeader'
import ResourceNotFound from '../../components/layout/ResourceNotFound'
import ClusterForm from '../../components/cluster/ClusterForm'
import { paths } from '../../routes/paths'
import layout from '../../components/layout/layout.module.css'

/** Full-page create (/clusters/new[?namespace=ns]) and edit (/clusters/:ns/:name/edit) screen. */
export default function ClusterEditor() {
  const { namespace, name } = useParams()
  const [search] = useSearchParams()
  const navigate = useNavigate()
  const clusters = useClusters()
  // Read once: a new form keeps what is typed into it.
  const [initialNamespace] = useState(() => search.get('namespace') ?? '')
  const [dirty, setDirty] = useState(false)
  const { allowNavigation, dialog } = useUnsavedChangesGuard(dirty)

  const isEdit = !!name
  const cluster = isEdit ? clusters?.find((c) => c.namespace === namespace && c.name === name) : undefined

  if (isEdit && !cluster) {
    return (
      <ResourceNotFound kind="Cluster" listPath={paths.clusters()} listLabel="Clusters" namespace={namespace} name={name} loading={clusters === null} />
    )
  }

  async function handleSubmit(data: ClusterFormData) {
    if (cluster) await updateCluster(cluster.namespace, cluster.name, data)
    else await createCluster(data)
    allowNavigation()
    navigate(paths.cluster(data.namespace, data.name))
  }

  const back = cluster ? paths.cluster(cluster.namespace, cluster.name) : paths.clusters()

  return (
    <div className={layout.page}>
      <PageHeader
        breadcrumbs={
          cluster
            ? [{ label: 'Clusters', to: paths.clusters() }, { label: cluster.namespace }, { label: cluster.name, to: back }]
            : [{ label: 'Clusters', to: paths.clusters() }]
        }
        title={cluster ? 'Edit Cluster' : 'New Cluster'}
        subtitle="The Environments of its namespace select it by its labels; Kokumi registers it with Argo CD for them."
      />
      <ClusterForm
        key={cluster ? `${cluster.namespace}/${cluster.name}` : 'new'}
        cluster={cluster}
        initialNamespace={initialNamespace}
        onSubmit={handleSubmit}
        onCancel={() => navigate(back)}
        onDirtyChange={setDirty}
      />
      {dialog}
    </div>
  )
}
