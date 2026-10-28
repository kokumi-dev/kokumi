import { Link } from 'react-router'
import type { Serving } from '../../api/types'
import Section from '../../components/layout/Section'
import EmptyState from '../../components/layout/EmptyState'
import DataTable from '../../components/layout/DataTable'
import NameCell from '../../components/layout/NameCell'
import StatusIndicator from '../../components/shared/StatusIndicator'
import RelativeTime from '../../components/shared/RelativeTime'
import { paths } from '../../routes/paths'
import { targetsSummary, targetsTitle } from '../../utils/clusters'
import { servingOf } from '../../utils/promotion'
import { stateTone } from '../../utils/state'
import { useOrderContext } from './orderContext'
import layout from '../../components/layout/layout.module.css'

export default function OrderServings() {
  const { order, servings } = useOrderContext()
  const serving = servingOf(order, servings)

  return (
    <>
      <Section title="Servings" description="Where this Order is deployed through Argo CD" flush>
        {servings === null ? (
          <EmptyState text="Loading…" />
        ) : servings.length === 0 ? (
          <EmptyState text="Nothing is served yet. Promote a Preparation to create a Serving." />
        ) : (
          <DataTable<Serving>
            rows={servings}
            rowKey={(s) => s.name}
            rowHref={(s) => paths.serving(s.namespace, s.name)}
            columns={[
              {
                header: 'Name',
                width: '30%',
                cell: (s) => <NameCell to={paths.serving(s.namespace, s.name)} name={s.name} secondary={s.namespace} />,
              },
              { header: 'Status', width: '130px', cell: (s) => <StatusIndicator state={s.state} tone={stateTone(s)} /> },
              {
                header: 'Deployed',
                truncate: true,
                title: (s) => s.deployedPreparation,
                cell: (s) =>
                  s.deployedPreparation ? (
                    <Link className={layout.refLink} to={paths.preparation(s.namespace, s.deployedPreparation)}>
                      {s.deployedPreparation}
                    </Link>
                  ) : (
                    <span className={layout.muted}>—</span>
                  ),
              },
              {
                header: 'Clusters',
                width: '18%',
                truncate: true,
                title: (s) => targetsTitle(s.clusters),
                cell: (s) => targetsSummary(s.clusters) ?? <span className={layout.muted}>—</span>,
              },
              { header: 'Created', width: '110px', cell: (s) => <RelativeTime iso={s.createdAt} /> },
            ]}
          />
        )}
      </Section>
      {serving && (
        <p className={layout.pageNote}>
          The Serving{' '}
          <Link className={layout.inlineLink} to={paths.serving(serving.namespace, serving.name)}>{serving.name}</Link>{' '}
          lists every cluster this Order deploys to, with what Argo CD reports there.
        </p>
      )}
    </>
  )
}
