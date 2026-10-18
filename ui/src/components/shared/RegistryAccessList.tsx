import { Link } from 'react-router'
import type { RegistryAccess } from '../../api/types'
import DataTable from '../layout/DataTable'
import EmptyState from '../layout/EmptyState'
import { paths } from '../../routes/paths'
import { stripOCIScheme } from '../../utils/format'
import layout from '../layout/layout.module.css'

const clientLabels: Record<RegistryAccess['client'], string> = {
  Kokumi: 'Kokumi',
  ArgoCD: 'Argo CD',
  Kubelet: 'Kubelet',
}

/**
 * RegistryAccessList shows the registry locations an object needs, who
 * pulls or pushes them, and the Registry Kokumi connects through. Argo CD and
 * the kubelet need their own credentials for their entries.
 */
export default function RegistryAccessList({ access, namespace }: { access?: RegistryAccess[]; namespace: string }) {
  if (!access?.length) return <EmptyState text="No registry access recorded yet" />
  return (
    <DataTable<RegistryAccess>
      rows={access}
      rowKey={(a) => `${a.client}|${a.operation}|${a.url}`}
      columns={[
        { header: 'Location', truncate: true, title: (a) => a.url, cell: (a) => <span className={layout.mono}>{stripOCIScheme(a.url)}</span> },
        { header: 'Operation', width: '120px', cell: (a) => a.operation },
        { header: 'Client', width: '100px', cell: (a) => clientLabels[a.client] ?? a.client },
        {
          header: 'Registry',
          width: '22%',
          truncate: true,
          title: (a) => a.registry,
          cell: (a) =>
            a.registry ? (
              <Link to={paths.registry(namespace, a.registry)}>{a.registry}</Link>
            ) : a.client === 'Kokumi' ? (
              <span className={layout.muted}>Anonymous</span>
            ) : (
              <span className={layout.muted}>Own credentials</span>
            ),
        },
      ]}
    />
  )
}
