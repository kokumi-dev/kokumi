import { useState } from 'react'
import { Outlet, useNavigate, useParams } from 'react-router'
import { deleteEnvironment } from '../../api/client'
import { useClusters, useEnvironments } from '../../hooks/useEnvironments'
import { useOrders } from '../../hooks/useOrders'
import { useDeniedReason } from '../../hooks/useCapability'
import PageHeader from '../../components/layout/PageHeader'
import ResourceNotFound from '../../components/layout/ResourceNotFound'
import Badge from '../../components/shared/Badge'
import ConfirmDialog from '../../components/shared/ConfirmDialog'
import { Button, ButtonLink, EditIcon, TabNav, TrashIcon } from '../../components/ui'
import { paths } from '../../routes/paths'
import { stateTone } from '../../utils/state'
import type { EnvironmentContext } from './environmentContext'
import layout from '../../components/layout/layout.module.css'

export default function EnvironmentLayout() {
  const { namespace = '', name = '' } = useParams()
  const navigate = useNavigate()
  const environments = useEnvironments()
  const allClusters = useClusters()
  const allOrders = useOrders()
  const [confirmDelete, setConfirmDelete] = useState(false)
  const editDenied = useDeniedReason('environments:update')
  const deleteDenied = useDeniedReason('environments:delete')

  const environment = environments?.find((e) => e.namespace === namespace && e.name === name)
  if (!environment) {
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

  const clusters = allClusters?.filter((c) => c.namespace === namespace) ?? null
  const orders = allOrders?.filter((o) => o.namespace === namespace && o.environmentRef.name === name) ?? null
  const requiredBy = (environments ?? []).filter(
    (e) => e.namespace === namespace && e.promotion?.requireAfter?.some((r) => r.environment === name),
  )
  const ctx: EnvironmentContext = { environment, clusters, orders, requiredBy }

  return (
    <div className={layout.page}>
      <PageHeader
        breadcrumbs={[{ label: 'Environments', to: paths.environments() }, { label: environment.namespace }]}
        title={environment.name}
        badge={<Badge state={environment.state} tone={stateTone(environment)} />}
        subtitle={environment.description}
        actions={
          <>
            <ButtonLink variant="secondary" icon={<EditIcon />} to={paths.environment(namespace, name, 'edit')} disabledReason={editDenied}>Edit</ButtonLink>
            <Button variant="danger" icon={<TrashIcon />} onClick={() => setConfirmDelete(true)} disabledReason={deleteDenied}>Delete</Button>
          </>
        }
      />

      <TabNav
        tabs={[
          { to: paths.environment(namespace, name), label: 'Overview', end: true },
          { to: paths.environment(namespace, name, 'clusters'), label: 'Clusters', count: environment.selectedClusters },
          { to: paths.environment(namespace, name, 'orders'), label: 'Orders', count: orders?.length },
        ]}
      />

      <Outlet context={ctx} />

      {confirmDelete && (
        <ConfirmDialog
          title={`Delete Environment ${environment.name}`}
          confirmLabel="Delete Environment"
          variant="danger"
          onCancel={() => setConfirmDelete(false)}
          onConfirm={async () => {
            await deleteEnvironment(namespace, name)
            navigate(paths.environments())
          }}
        >
          {environment.ensured ? (
            <>
              Kokumi keeps <strong>{namespace}/{name}</strong> and creates it again with its defaults: the cluster Kokumi
              runs in, through Argo CD&apos;s default project, and its Orders deploy there again. To restrict it, edit it
              instead.
            </>
          ) : orders?.length ? (
            <>
              <strong>{orders.length === 1 ? 'One Order deploys' : `${orders.length} Orders deploy`}</strong> to{' '}
              <strong>{namespace}/{name}</strong>: {orders.map((o) => o.name).join(', ')}. They stop deploying and report
              that the Environment does not exist, until it is created again; what runs on its clusters keeps running.
            </>
          ) : (
            <>No Order deploys to <strong>{namespace}/{name}</strong>. What runs on its clusters is not touched.</>
          )}
          {requiredBy.length > 0 && (
            <>
              {' '}{requiredBy.map((e) => e.name).join(', ')} {requiredBy.length === 1 ? 'requires' : 'require'} running after it:
              their Orders wait for upstream Orders here that no longer deploy new revisions.
            </>
          )}
        </ConfirmDialog>
      )}
    </div>
  )
}
