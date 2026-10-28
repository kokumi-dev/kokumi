import { Link } from 'react-router'
import type { Order, Preparation, Product, Serving } from '../../api/types'
import Section from '../../components/layout/Section'
import DataTable from '../../components/layout/DataTable'
import NameCell from '../../components/layout/NameCell'
import Badge from '../../components/shared/Badge'
import RelativeTime from '../../components/shared/RelativeTime'
import { paths } from '../../routes/paths'
import { formatDuration } from '../../utils/format'
import { deployedEntry, pipelineStages, servingOf, shortRevision, upstreamVerdict, withinAMinute, type PipelineStage } from '../../utils/promotion'
import layout from '../../components/layout/layout.module.css'

/** The Orders order runs after, with their soaks, e.g. "podinfo-staging (2 minutes)". */
function upstreamsText(order: Order): string {
  return (order.after ?? []).map((u) => `${u.order}${u.soakSeconds ? ` (${formatDuration(u.soakSeconds)})` : ''}`).join(', ')
}

interface Props {
  order: Order
  product?: Product
  /** All Orders and Servings; null until loaded. */
  orders: Order[] | null
  servings: Serving[] | null
  /** The newest Preparation of the Order, whose upstream gate the section explains. */
  latest?: Preparation
}

/**
 * OrderPipeline shows the stages of the Order's promotion pipeline: the
 * Orders of its Product it runs after and that run after it, each with the
 * revision it deploys and since when it is healthy, and what holds the
 * Order's newest Preparation.
 */
export default function OrderPipeline({ order, product, orders, servings, latest }: Props) {
  if (!orders) return null
  const stages = pipelineStages(order, orders)
  if (stages.length < 2) return null

  const verdict = latest && latest.name !== order.activePreparation ? upstreamVerdict(latest, order, orders, servings) : null

  return (
    <Section
      title="Pipeline"
      description="Every stage renders and deploys its own Preparation of a Product revision, once the stages it runs after deployed theirs"
      flush
    >
      <DataTable<PipelineStage>
        rows={stages}
        rowKey={(s) => s.order.name}
        rowHref={(s) => paths.order(s.order.namespace, s.order.name)}
        isRowSelected={(s) => s.order.name === order.name}
        columns={[
          {
            header: 'Stage',
            width: '20%',
            cell: (s) => <NameCell to={paths.order(s.order.namespace, s.order.name)} name={s.order.name} secondary={[s.order.environmentRef.name, s.order.mode].join(' · ')} />,
          },
          {
            header: 'Runs after',
            width: '20%',
            truncate: true,
            title: (s) => upstreamsText(s.order),
            cell: (s) => upstreamsText(s.order) || <span className={layout.muted}>—</span>,
          },
          {
            header: 'Deploys',
            truncate: true,
            title: (s) => deployedEntry(servingOf(s.order, servings))?.preparation,
            cell: (s) => {
              const deployed = deployedEntry(servingOf(s.order, servings))
              return deployed ? (
                <Link className={layout.refLink} to={paths.preparation(s.order.namespace, deployed.preparation)}>
                  {deployed.preparation}
                </Link>
              ) : (
                <span className={layout.muted}>Nothing yet</span>
              )
            },
          },
          {
            header: 'Revision',
            width: '190px',
            title: (s) => deployedEntry(servingOf(s.order, servings))?.productRevision,
            cell: (s) => {
              const deployed = deployedEntry(servingOf(s.order, servings))
              if (!deployed) return <span className={layout.muted}>—</span>
              return (
                <>
                  <span className={layout.mono}>{shortRevision(deployed.productRevision).slice(0, 8)}</span>{' '}
                  {product?.revision && deployed.productRevision === product.revision && (
                    <Badge state="Current" tone="success" title="Runs the Product revision its Orders render now" />
                  )}
                </>
              )
            },
          },
          {
            header: 'Healthy since',
            width: '140px',
            cell: (s) => {
              const deployed = deployedEntry(servingOf(s.order, servings))
              if (deployed?.endReason === 'Degraded') return <Badge state="Degraded" tone="danger" />
              return deployed?.healthyAt ? <RelativeTime iso={deployed.healthyAt} /> : <span className={layout.muted}>—</span>
            },
          },
        ]}
      />
      {verdict && (
        <p className={layout.muted} style={{ padding: '10px 16px', margin: 0 }}>
          Newest Preparation <Link className={layout.inlineLink} to={paths.preparation(latest!.namespace, latest!.name)}>{latest!.name}</Link>:{' '}
          <Badge state={verdict.passes ? 'Ready' : verdict.outcome} tone={verdict.tone} title={verdict.text} /> {verdict.text}
          {verdict.stages
            .filter((s) => s.soakEndsAt)
            .map((s) => (
              <span key={s.stage.order}>
                {' '}· soak after {s.stage.order} ends{' '}
                {withinAMinute(s.soakEndsAt!) ? 'within a minute' : <RelativeTime iso={s.soakEndsAt} />}
              </span>
            ))}
        </p>
      )}
    </Section>
  )
}
