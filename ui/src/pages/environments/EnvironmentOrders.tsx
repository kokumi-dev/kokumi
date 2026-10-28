import { Link } from 'react-router'
import type { Order } from '../../api/types'
import Section from '../../components/layout/Section'
import DataTable from '../../components/layout/DataTable'
import EmptyState from '../../components/layout/EmptyState'
import NameCell from '../../components/layout/NameCell'
import StatusIndicator from '../../components/shared/StatusIndicator'
import { paths } from '../../routes/paths'
import { targetNamespaceSource } from '../../utils/products'
import { stateTone } from '../../utils/state'
import { useEnvironmentContext } from './environmentContext'
import layout from '../../components/layout/layout.module.css'

export default function EnvironmentOrders() {
  const { environment, orders } = useEnvironmentContext()

  return (
    <Section title="Orders" description={`The Orders of namespace ${environment.namespace} that deploy here`} flush>
      {orders === null ? (
        <EmptyState text="Loading…" />
      ) : orders.length === 0 ? (
        <EmptyState text="No Order deploys here yet: an Order names its Environment when it is created." />
      ) : (
        <DataTable<Order>
          rows={orders}
          rowKey={(o) => o.name}
          rowHref={(o) => paths.order(o.namespace, o.name)}
          columns={[
            { header: 'Order', width: '26%', cell: (o) => <NameCell to={paths.order(o.namespace, o.name)} name={o.name} /> },
            { header: 'Status', width: '130px', cell: (o) => <StatusIndicator state={o.state} tone={stateTone(o)} /> },
            {
              header: 'Product',
              truncate: true,
              title: (o) => o.productRef.name,
              cell: (o) => (
                <Link className={layout.refLink} to={paths.product(o.namespace, o.productRef.name)}>{o.productRef.name}</Link>
              ),
            },
            {
              header: 'Target namespace',
              width: '24%',
              truncate: true,
              title: (o) => `${o.effectiveTargetNamespace}, ${targetNamespaceSource(o.targetNamespaceSource)}`,
              cell: (o) => (
                <>
                  <span className={layout.mono}>{o.effectiveTargetNamespace}</span>
                  <span className={layout.secondaryText}>{targetNamespaceSource(o.targetNamespaceSource)}</span>
                </>
              ),
            },
          ]}
        />
      )}
    </Section>
  )
}
