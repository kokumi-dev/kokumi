import { Link, useParams } from 'react-router'
import { useServings } from '../../hooks/useServings'
import { argoAppURL, useArgoCDBase } from '../../hooks/useArgoCDBase'
import PageHeader from '../../components/layout/PageHeader'
import Section from '../../components/layout/Section'
import PropertyList from '../../components/layout/PropertyList'
import ConditionList from '../../components/layout/ConditionList'
import ResourceNotFound from '../../components/layout/ResourceNotFound'
import Badge from '../../components/shared/Badge'
import { paths } from '../../routes/paths'
import { formatDate } from '../../utils/format'
import layout from '../../components/layout/layout.module.css'

export default function ServingDetail() {
  const { namespace = '', name = '' } = useParams()
  const servings = useServings()
  const argoBase = useArgoCDBase()

  const serving = servings?.find((s) => s.namespace === namespace && s.name === name)
  if (!serving) {
    return (
      <ResourceNotFound
        kind="Serving"
        listPath={paths.servings()}
        listLabel="Servings"
        namespace={namespace}
        name={name}
        loading={servings === null}
      />
    )
  }

  const target = serving.targetPreparation || serving.desiredPreparation
  const inSync = !!target && target === serving.observedPreparation
  const argoURL = argoAppURL(argoBase, serving.name)
  const gate = serving.conditions?.find((c) => c.type === 'Approved')

  const prepLink = (prepName?: string) =>
    prepName ? (
      <Link className={`${layout.mono} ${layout.inlineLink}`} to={paths.preparation(serving.namespace, prepName)}>{prepName}</Link>
    ) : (
      <span className={layout.muted}>—</span>
    )

  return (
    <div className={layout.page}>
      <PageHeader
        breadcrumbs={[
          { label: 'Servings', to: paths.servings() },
          { label: serving.namespace },
          { label: serving.order, to: paths.order(serving.namespace, serving.order, 'servings') },
        ]}
        title={serving.name}
        badge={<Badge state={serving.state} />}
        actions={argoURL && (
          <a className={layout.inlineLink} href={argoURL} target="_blank" rel="noopener noreferrer">Open in Argo CD ↗</a>
        )}
      />

      <Section title="Deployment" description={inSync ? 'The deployed Preparation matches the target.' : 'Waiting for the target Preparation to be deployed.'}>
        <PropertyList
          items={[
            {
              label: 'Order',
              value: <Link className={layout.inlineLink} to={paths.order(serving.namespace, serving.order)}>{serving.order}</Link>,
            },
            { label: 'Target Preparation', value: prepLink(target) },
            { label: 'Deployed Preparation', value: prepLink(serving.observedPreparation) },
            {
              label: 'Deployed digest',
              value: <span className={layout.mono}>{serving.deployedDigest}</span>,
              show: !!serving.deployedDigest,
            },
            { label: 'Approval gate', value: gate?.reason ?? '—', show: !!gate },
            { label: 'Created', value: formatDate(serving.createdAt) },
          ]}
        />
      </Section>

      <Section title="Conditions">
        <ConditionList conditions={serving.conditions} />
      </Section>
    </div>
  )
}
