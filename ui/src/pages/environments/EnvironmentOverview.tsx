import { Link } from 'react-router'
import Section from '../../components/layout/Section'
import PropertyList from '../../components/layout/PropertyList'
import ConditionList from '../../components/layout/ConditionList'
import { paths } from '../../routes/paths'
import { formatDate, formatDuration, plural } from '../../utils/format'
import { describeSelector } from '../../utils/selectors'
import { environmentClustersSummary } from '../../utils/clusters'
import { useEnvironmentContext } from './environmentContext'
import layout from '../../components/layout/layout.module.css'

export default function EnvironmentOverview() {
  const { environment, orders, requiredBy } = useEnvironmentContext()
  const { namespace, name } = environment
  const required = environment.promotion?.requireAfter ?? []

  return (
    <>
      <p className={layout.pageNote}>
        <Link className={layout.inlineLink} to={paths.environment(namespace, name, 'clusters')}>
          {environmentClustersSummary(environment)}
        </Link>
        {orders && (
          <>
            {' · '}
            <Link className={layout.inlineLink} to={paths.environment(namespace, name, 'orders')}>{plural(orders.length, 'Order')}</Link>
          </>
        )}
      </p>

      {environment.ensured && (
        <p className={layout.pageNote}>
          Kokumi keeps this Environment: it comes back when deleted. To restrict where the Orders of {namespace} deploy, edit it.
        </p>
      )}

      <Section title="Environment" description="Where the Orders of its namespace that name it deploy, and what they must pass first">
        <PropertyList
          items={[
            { label: 'Namespace', value: namespace },
            { label: 'Deployer', value: `Argo CD, project ${environment.deployer.argoCD?.project ?? '—'}` },
            {
              label: 'Cluster selector',
              value: <span className={layout.mono}>{describeSelector(environment.clusterSelector, 'Every Cluster of the namespace')}</span>,
            },
            {
              label: 'Requires',
              value: required.length ? (
                required.map((r, i) => (
                  <span key={r.environment}>
                    {i > 0 && '; '}
                    <Link className={layout.inlineLink} to={paths.environment(namespace, r.environment)}>{r.environment}</Link>
                    {r.minSoakSeconds ? `, after a soak of at least ${formatDuration(r.minSoakSeconds)}` : ''}
                  </span>
                ))
              ) : (
                <span className={layout.muted}>Nothing: Orders deploy here without upstream stages</span>
              ),
            },
            {
              label: 'Required by',
              value: requiredBy.map((e, i) => (
                <span key={e.name}>
                  {i > 0 && ', '}
                  <Link className={layout.inlineLink} to={paths.environment(namespace, e.name)}>{e.name}</Link>
                </span>
              )),
              show: requiredBy.length > 0,
            },
            { label: 'Created', value: formatDate(environment.createdAt) },
          ]}
        />
      </Section>

      <Section title="Conditions">
        <ConditionList conditions={environment.conditions} />
      </Section>
    </>
  )
}
