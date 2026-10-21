import { Link } from 'react-router'
import type { Order, Preparation, Product, Serving } from '../../api/types'
import GraphView, { type Note } from '../../components/graph/GraphView'
import LayeredGraph, { type GraphEdge, type GraphNode } from '../../components/graph/LayeredGraph'
import { Chip, NodeCard, NodeIcon, NodeSummary, TagIcon, type Status } from '../../components/graph/parts'
import { useGraphSelection } from '../../components/graph/useGraphSelection'
import { paths } from '../../routes/paths'
import { pendingLabel, pendingText } from '../../utils/promotion'
import OrderDetails from './OrderDetails'
import { environmentLayout } from './environments'
import { OrderIcon } from './orderIcons'
import { orderInsights, stateStatus, type OrderInsight } from './orderInsights'
import styles from './OrderGraph.module.css'

interface Props {
  product: Product
  /** The Orders of the Product, sorted by name; null while loading. */
  orders: Order[] | null
  /** null while loading; they hold the versions the Orders run. */
  preparations: Preparation[] | null
  /** null while loading; they hold what the Orders' latest Preparations wait for. */
  servings: Serving[] | null
  /** Why no Order can be created now; undefined when it can. */
  orderDenied?: string
}

const newOrderNode = 'new-order'
const orderNode = (name: string) => `order:${name}`

/**
 * OrderGraph draws the Orders of a Product from left to right, each after
 * the Orders it runs after, and the details of the Order selected beside
 * them. When Orders name their environment, each environment is a column,
 * its Orders stacked by wave, and only the edges between environments are
 * drawn. A Product without Orders shows a link to create the first; the
 * page header offers it otherwise.
 */
export default function OrderGraph({ product, orders, preparations, servings, orderDenied }: Props) {
  const { selected, toggle, clear } = useGraphSelection()
  const environments = environmentLayout(orders ?? [])
  const layout = environments && 'orders' in environments ? environments : undefined
  const insights = orderInsights(layout?.orders ?? orders ?? [], preparations, servings, product.revision)
  const current = insights.find((i) => orderNode(i.order.name) === selected)
  const names = new Set(insights.map((i) => i.order.name))

  const nodes: GraphNode[] =
    insights.length === 0
      ? [{ id: newOrderNode, card: <FirstOrderCard product={product} denied={orderDenied} /> }]
      : insights.map((insight) => ({
          id: orderNode(insight.order.name),
          card: (
            <OrderCard
              insight={insight}
              wave={layout?.wave.get(insight.order.name)}
              selected={insight === current}
              onSelect={() => toggle(orderNode(insight.order.name))}
            />
          ),
        }))
  const edges: GraphEdge[] = insights.flatMap(({ order }) =>
    (order.after ?? [])
      .filter((s) => names.has(s.order) && !layout?.inside(s.order, order.name))
      .map((s) => ({ from: orderNode(s.order), to: orderNode(order.name) })),
  )
  const notes: Note[] =
    environments && 'conflict' in environments
      ? [{ tone: 'warning', text: `${environments.conflict} The graph shows the Orders by the Orders they run after instead.` }]
      : []

  return (
    <GraphView
      loading={orders === null}
      inspector={current && <OrderDetails key={current.order.name} insight={current} onClose={clear} />}
      notes={notes}
    >
      <LayeredGraph
        nodes={nodes}
        edges={edges}
        rank={layout && ((id) => layout.column.get(id.slice('order:'.length)) ?? 0)}
        titles={layout?.titles}
        label={`The Orders of ${product.name}`}
      />
    </GraphView>
  )
}

/**
 * What reaches the Order: what its latest Preparation waits for, or that it
 * runs the current Product revision; undefined when neither applies.
 */
function deliveryStatus({ pending, currentRevision }: OrderInsight): Status | undefined {
  if (pending) {
    const unpromoted = pending.kind === 'unpromoted'
    return {
      tone: unpromoted ? 'accent' : 'warning',
      icon: unpromoted ? 'arrow' : 'clock',
      label: pendingLabel(pending),
      title: pendingText(pending),
    }
  }
  if (currentRevision) {
    return { tone: 'success', icon: 'revision', label: 'Current revision', title: 'Runs the Product revision its Orders render now' }
  }
  return undefined
}

/** The wave of an Order in its environment, as a tooltip says it. */
function waveText(environment: string | undefined, { wave, after }: { wave: number; after: string[] }): string {
  return `Wave ${wave} of ${environment}: ${after.length > 0 ? `runs after ${after.join(', ')}` : 'runs first'}`
}

/**
 * An Order: its state, the version it runs and what its latest Preparation
 * waits for, and its wave in an environment of several waves.
 */
function OrderCard({
  insight,
  wave,
  selected,
  onSelect,
}: {
  insight: OrderInsight
  wave?: { wave: number; after: string[] }
  selected: boolean
  onSelect: () => void
}) {
  const { order } = insight
  return (
    <NodeCard nodeKey={orderNode(order.name)} selected={selected} onClick={onSelect}>
      <NodeSummary
        icon={<OrderIcon muted={!insight.active} />}
        title={
          wave ? (
            <>
              <span className={styles.wave} title={waveText(order.environment, wave)}>{wave.wave}</span>
              {order.name}
            </>
          ) : (
            order.name
          )
        }
        status={stateStatus(order)}
        detail={deliveryStatus(insight)}
      />
      {insight.activeVersion ? (
        <Chip icon={<TagIcon />} mono className={styles.trail} title={`Runs ${insight.active}`}>{insight.activeVersion}</Chip>
      ) : !insight.active && (
        <span className={`${styles.trail} ${styles.muted}`}>Not deployed</span>
      )}
    </NodeCard>
  )
}

/** The first Order of a Product without Orders: a link to the form. */
function FirstOrderCard({ product, denied }: { product: Product; denied?: string }) {
  const content = (
    <>
      <NodeIcon tone={denied ? 'muted' : 'accent'}>
        <path d="M8 3.5v9M3.5 8h9" />
      </NodeIcon>
      Create the first Order
    </>
  )
  if (denied) {
    return <span className={`${styles.add} ${styles.addDisabled}`} role="link" aria-disabled="true" tabIndex={0} title={denied}>{content}</span>
  }
  return <Link className={styles.add} to={paths.newOrder(product)}>{content}</Link>
}
