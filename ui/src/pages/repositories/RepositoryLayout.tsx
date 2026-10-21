import { useState } from 'react'
import { Outlet, useNavigate, useParams } from 'react-router'
import { deleteRepository } from '../../api/client'
import { useRepositories } from '../../hooks/useRegistries'
import { useDeniedReason } from '../../hooks/useCapability'
import PageHeader from '../../components/layout/PageHeader'
import ResourceNotFound from '../../components/layout/ResourceNotFound'
import Badge from '../../components/shared/Badge'
import ConfirmDialog from '../../components/shared/ConfirmDialog'
import { Button, ButtonLink, EditIcon, PlusIcon, TabNav, TrashIcon } from '../../components/ui'
import { paths } from '../../routes/paths'
import { stateTone } from '../../utils/state'
import layout from '../../components/layout/layout.module.css'

export default function RepositoryLayout() {
  const { namespace = '', name = '' } = useParams()
  const navigate = useNavigate()
  const repositories = useRepositories()
  const [confirmDelete, setConfirmDelete] = useState(false)
  const createProductDenied = useDeniedReason('products:create')
  const editDenied = useDeniedReason('repositories:update')
  const deleteDenied = useDeniedReason('repositories:delete')

  const repository = repositories?.find((r) => r.namespace === namespace && r.name === name)
  if (!repository) {
    return (
      <ResourceNotFound kind="Repository" listPath={paths.repositories()} listLabel="Repositories" namespace={namespace} name={name} loading={repositories === null} />
    )
  }

  return (
    <div className={layout.page}>
      <PageHeader
        breadcrumbs={[{ label: 'Repositories', to: paths.repositories() }, { label: repository.namespace }]}
        title={repository.name}
        badge={<Badge state={repository.state ?? ''} tone={stateTone(repository)} />}
        subtitle={<span className={layout.mono}>{repository.url}</span>}
        actions={
          <>
            <ButtonLink variant="primary" icon={<PlusIcon />} to={paths.newProduct(repository)} disabledReason={createProductDenied}>Create Product</ButtonLink>
            <ButtonLink variant="secondary" icon={<EditIcon />} to={paths.repository(repository.namespace, repository.name, 'edit')} disabledReason={editDenied}>Edit</ButtonLink>
            <Button variant="danger" icon={<TrashIcon />} onClick={() => setConfirmDelete(true)} disabledReason={deleteDenied}>Delete</Button>
          </>
        }
      />

      <TabNav
        tabs={[
          { to: paths.repository(repository.namespace, repository.name), label: 'Overview', end: true },
          { to: paths.repository(repository.namespace, repository.name, 'tags'), label: 'Tags' },
        ]}
      />

      <Outlet context={{ repository }} />

      {confirmDelete && (
        <ConfirmDialog
          title={`Delete Repository ${repository.name}`}
          confirmLabel="Delete Repository"
          variant="danger"
          onCancel={() => setConfirmDelete(false)}
          onConfirm={async () => {
            await deleteRepository(repository.namespace, repository.name)
            navigate(paths.repositories())
          }}
        >
          This removes <strong>{repository.namespace}/{repository.name}</strong> from the catalog. Products created from it and
          artifacts in the registry are not touched.
        </ConfirmDialog>
      )}
    </div>
  )
}
