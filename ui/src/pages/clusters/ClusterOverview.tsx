import { Link } from 'react-router'
import Section from '../../components/layout/Section'
import PropertyList from '../../components/layout/PropertyList'
import ConditionList from '../../components/layout/ConditionList'
import LabelList from '../../components/shared/LabelList'
import { paths } from '../../routes/paths'
import { formatDate } from '../../utils/format'
import { useClusterContext } from './clusterContext'
import layout from '../../components/layout/layout.module.css'

export default function ClusterOverview() {
  const { cluster } = useClusterContext()
  const { namespace } = cluster

  return (
    <>
      {cluster.ensured && (
        <p className={layout.pageNote}>
          Kokumi keeps this Cluster in its own namespace, so that the Environment local can select it. Its labels and
          description can be changed; deleted, it comes back with its defaults.
        </p>
      )}

      <Section title="Cluster">
        <PropertyList
          items={[
            { label: 'Namespace', value: namespace },
            {
              label: 'Connection',
              value:
                cluster.connection === 'InCluster' ? (
                  'The cluster Kokumi runs in'
                ) : cluster.kubeconfigSecret ? (
                  <>Kubeconfig in Secret <span className={layout.mono}>{cluster.kubeconfigSecret}</span></>
                ) : (
                  'Kubeconfig'
                ),
            },
            {
              label: 'Server',
              value: cluster.server ? <span className={layout.mono}>{cluster.server}</span> : <span className={layout.muted}>Not known yet</span>,
            },
            { label: 'Kubernetes version', value: cluster.kubernetesVersion || <span className={layout.muted}>Not known yet</span> },
            {
              label: 'State',
              value: (
                <>
                  {cluster.state}
                  {cluster.message && <span className={layout.muted}>: {cluster.message}</span>}
                </>
              ),
            },
            { label: 'Labels', value: <LabelList labels={cluster.labels} /> },
            {
              label: 'Environments',
              value: cluster.environments?.length ? (
                cluster.environments.map((e, i) => (
                  <span key={e}>
                    {i > 0 && ', '}
                    <Link className={layout.inlineLink} to={paths.environment(namespace, e)}>{e}</Link>
                  </span>
                ))
              ) : (
                <span className={layout.muted}>No Environment of {namespace} selects it</span>
              ),
            },
            { label: 'Created', value: formatDate(cluster.createdAt) },
          ]}
        />
      </Section>

      <Section title="Conditions">
        <ConditionList conditions={cluster.conditions} />
      </Section>
    </>
  )
}
