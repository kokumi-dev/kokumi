import { useState } from 'react'
import { useNavigate, useParams } from 'react-router'
import { deleteRegistry } from '../../api/client'
import { registryCredentialsSecret } from '../../api/types'
import { useRegistries } from '../../hooks/useRegistries'
import { useDeniedReason } from '../../hooks/useCapability'
import PageHeader from '../../components/layout/PageHeader'
import ResourceNotFound from '../../components/layout/ResourceNotFound'
import Section from '../../components/layout/Section'
import PropertyList from '../../components/layout/PropertyList'
import ConditionList from '../../components/layout/ConditionList'
import Badge from '../../components/shared/Badge'
import ConfirmDialog from '../../components/shared/ConfirmDialog'
import { Button, ButtonLink, EditIcon, TrashIcon } from '../../components/ui'
import { paths } from '../../routes/paths'
import { formatDate } from '../../utils/format'
import layout from '../../components/layout/layout.module.css'

export default function RegistryDetail() {
  const { namespace = '', name = '' } = useParams()
  const navigate = useNavigate()
  const registries = useRegistries()
  const [confirmDelete, setConfirmDelete] = useState(false)
  const editDenied = useDeniedReason('registries:update')
  const deleteDenied = useDeniedReason('registries:delete')

  const registry = registries?.find((r) => r.namespace === namespace && r.name === name)
  if (!registry) {
    return (
      <ResourceNotFound kind="Registry" listPath={paths.registries()} listLabel="Registries" namespace={namespace} name={name} loading={registries === null} />
    )
  }
  const ownsSecret = registry.secretRef === registryCredentialsSecret(registry.name)

  return (
    <div className={layout.page}>
      <PageHeader
        breadcrumbs={[{ label: 'Registries', to: paths.registries() }, { label: registry.namespace }]}
        title={registry.name}
        badge={<Badge state={registry.state ?? ''} />}
        subtitle={<span className={layout.mono}>{registry.url}</span>}
        actions={
          <>
            <ButtonLink variant="secondary" icon={<EditIcon />} to={paths.registry(registry.namespace, registry.name, 'edit')} disabledReason={editDenied}>Edit</ButtonLink>
            <Button variant="danger" icon={<TrashIcon />} onClick={() => setConfirmDelete(true)} disabledReason={deleteDenied}>Delete</Button>
          </>
        }
      />

      <Section title="Connection">
        <PropertyList
          items={[
            { label: 'URL', value: <span className={layout.mono}>{registry.url}</span> },
            { label: 'Scheme', value: registry.scheme },
            {
              label: 'Credentials Secret',
              value: registry.secretRef
                ? <span className={layout.mono}>{registry.secretRef}{ownsSecret ? ' (stored by Kokumi)' : ''}</span>
                : <span className={layout.muted}>None: anonymous</span>,
            },
            {
              label: 'TLS Secret',
              value: registry.certSecretRef ? <span className={layout.mono}>{registry.certSecretRef}</span> : <span className={layout.muted}>None: system trust store</span>,
            },
            { label: 'Created', value: formatDate(registry.createdAt) },
          ]}
        />
      </Section>

      <Section title="Conditions">
        <ConditionList conditions={registry.conditions} />
      </Section>

      {confirmDelete && (
        <ConfirmDialog
          title={`Delete Registry ${registry.name}`}
          confirmLabel="Delete Registry"
          variant="danger"
          onCancel={() => setConfirmDelete(false)}
          onConfirm={async () => {
            await deleteRegistry(registry.namespace, registry.name)
            navigate(paths.registries())
          }}
        >
          Products and Orders that list <strong>{registry.namespace}/{registry.name}</strong> stop rendering until it is
          removed from their Registries.
          {ownsSecret && <> The Secret <strong>{registry.secretRef}</strong> it stored is deleted with it.</>} Artifacts in the
          registry are not touched.
        </ConfirmDialog>
      )}
    </div>
  )
}
