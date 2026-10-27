import { Outlet, useParams } from 'react-router'
import { useServings } from '../../hooks/useServings'
import { argoAppURL, useArgoCDBase } from '../../hooks/useArgoCDBase'
import PageHeader from '../../components/layout/PageHeader'
import ResourceNotFound from '../../components/layout/ResourceNotFound'
import Badge from '../../components/shared/Badge'
import { TabNav } from '../../components/ui'
import { paths } from '../../routes/paths'
import { stateTone } from '../../utils/state'
import type { ServingContext } from './servingContext'
import layout from '../../components/layout/layout.module.css'

export default function ServingLayout() {
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

  const argoURL = argoAppURL(argoBase, serving.applicationName)
  const ctx: ServingContext = { serving }

  return (
    <div className={layout.page}>
      <PageHeader
        breadcrumbs={[
          { label: 'Servings', to: paths.servings() },
          { label: serving.namespace },
          { label: serving.order, to: paths.order(serving.namespace, serving.order, 'servings') },
        ]}
        title={serving.name}
        badge={<Badge state={serving.state} tone={stateTone(serving)} />}
        actions={argoURL && (
          <a className={layout.inlineLink} href={argoURL} target="_blank" rel="noopener noreferrer">Open in Argo CD ↗</a>
        )}
      />

      <TabNav
        tabs={[
          { to: paths.serving(serving.namespace, serving.name), label: 'Overview', end: true },
          { to: paths.serving(serving.namespace, serving.name, 'history'), label: 'History', count: serving.history?.length ?? 0 },
        ]}
      />

      <Outlet context={ctx} />
    </div>
  )
}
