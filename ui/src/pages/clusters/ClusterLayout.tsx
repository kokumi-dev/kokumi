import { useState } from 'react'
import { Outlet, useNavigate, useParams } from 'react-router'
import { deleteCluster } from '../../api/client'
import { useClusters } from '../../hooks/useEnvironments'
import { useArgoCDBase } from '../../hooks/useArgoCDBase'
import { useDeniedReason } from '../../hooks/useCapability'
import PageHeader from '../../components/layout/PageHeader'
import ResourceNotFound from '../../components/layout/ResourceNotFound'
import Badge from '../../components/shared/Badge'
import ConfirmDialog from '../../components/shared/ConfirmDialog'
import { Button, ButtonLink, EditIcon, TabNav, TrashIcon } from '../../components/ui'
import { paths } from '../../routes/paths'
import { clusterTone } from '../../utils/clusters'
import type { ClusterContext } from './clusterContext'
import layout from '../../components/layout/layout.module.css'

export default function ClusterLayout() {
  const { namespace = '', name = '' } = useParams()
  const navigate = useNavigate()
  const clusters = useClusters()
  const argoBase = useArgoCDBase()
  const [confirmDelete, setConfirmDelete] = useState(false)
  const editDenied = useDeniedReason('clusters:update')
  const deleteDenied = useDeniedReason('clusters:delete')

  const cluster = clusters?.find((c) => c.namespace === namespace && c.name === name)
  if (!cluster) {
    return (
      <ResourceNotFound kind="Cluster" listPath={paths.clusters()} listLabel="Clusters" namespace={namespace} name={name} loading={clusters === null} />
    )
  }

  const ctx: ClusterContext = { cluster, argoBase }

  return (
    <div className={layout.page}>
      <PageHeader
        breadcrumbs={[{ label: 'Clusters', to: paths.clusters() }, { label: cluster.namespace }]}
        title={cluster.name}
        badge={<Badge state={cluster.state} tone={clusterTone(cluster)} title={cluster.message} />}
        subtitle={cluster.description}
        actions={
          <>
            <ButtonLink variant="secondary" icon={<EditIcon />} to={paths.cluster(namespace, name, 'edit')} disabledReason={editDenied}>Edit</ButtonLink>
            <Button variant="danger" icon={<TrashIcon />} onClick={() => setConfirmDelete(true)} disabledReason={deleteDenied}>Delete</Button>
          </>
        }
      />

      <TabNav
        tabs={[
          { to: paths.cluster(namespace, name), label: 'Overview', end: true },
          { to: paths.cluster(namespace, name, 'deployments'), label: 'Deployments' },
        ]}
      />

      <Outlet context={ctx} />

      {confirmDelete && (
        <ConfirmDialog
          title={`Delete Cluster ${cluster.name}`}
          confirmLabel="Delete Cluster"
          variant="danger"
          onCancel={() => setConfirmDelete(false)}
          onConfirm={async () => {
            await deleteCluster(namespace, name)
            navigate(paths.clusters())
          }}
        >
          This deletes <strong>{namespace}/{name}</strong>.
          {cluster.connection === 'Kubeconfig' && <> A kubeconfig Secret stored through Kokumi is deleted with it.</>}
          {cluster.ensured && (
            <> Kokumi keeps this Cluster of the cluster it runs in, and creates it again with its defaults.</>
          )}
          {cluster.environments?.length ? (
            <> The Environments {cluster.environments.join(', ')} stop deploying to it.</>
          ) : null}{' '}
          What runs on the cluster is not touched.
        </ConfirmDialog>
      )}
    </div>
  )
}
