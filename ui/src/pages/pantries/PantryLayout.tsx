import { useState } from 'react'
import { Outlet, useNavigate, useParams } from 'react-router'
import { deletePantry } from '../../api/client'
import { usePantries } from '../../hooks/usePantries'
import PageHeader from '../../components/layout/PageHeader'
import TabNav from '../../components/layout/TabNav'
import ResourceNotFound from '../../components/layout/ResourceNotFound'
import Badge from '../../components/shared/Badge'
import Btn, { BtnLink } from '../../components/shared/Btn'
import ConfirmDialog from '../../components/shared/ConfirmDialog'
import { paths } from '../../routes/paths'
import layout from '../../components/layout/layout.module.css'

export default function PantryLayout() {
  const { namespace = '', name = '' } = useParams()
  const navigate = useNavigate()
  const pantries = usePantries()
  const [confirmDelete, setConfirmDelete] = useState(false)

  const pantry = pantries?.find((p) => p.namespace === namespace && p.name === name)
  if (!pantry) {
    return (
      <ResourceNotFound kind="Pantry" listPath={paths.pantries()} listLabel="Pantries" namespace={namespace} name={name} loading={pantries === null} />
    )
  }

  return (
    <div className={layout.page}>
      <PageHeader
        breadcrumbs={[{ label: 'Pantries', to: paths.pantries() }, { label: pantry.namespace }]}
        title={pantry.name}
        badge={<Badge state={pantry.state ?? ''} />}
        subtitle={<span className={layout.mono}>{pantry.url}</span>}
        actions={
          <>
            <BtnLink variant="secondary" size="sm" to={paths.pantry(pantry.namespace, pantry.name, 'edit')}>Edit</BtnLink>
            <Btn variant="danger" size="sm" onClick={() => setConfirmDelete(true)}>Delete</Btn>
          </>
        }
      />

      <TabNav
        tabs={[
          { to: paths.pantry(pantry.namespace, pantry.name), label: 'Overview', end: true },
          { to: paths.pantry(pantry.namespace, pantry.name, 'tags'), label: 'Tags' },
        ]}
      />

      <Outlet context={{ pantry }} />

      {confirmDelete && (
        <ConfirmDialog
          title={`Delete Pantry ${pantry.name}`}
          confirmLabel="Delete Pantry"
          variant="danger"
          onCancel={() => setConfirmDelete(false)}
          onConfirm={async () => {
            await deletePantry(pantry.namespace, pantry.name)
            navigate(paths.pantries())
          }}
        >
          This deletes the connection <strong>{pantry.namespace}/{pantry.name}</strong>. Artifacts in the registry are not touched.
        </ConfirmDialog>
      )}
    </div>
  )
}
