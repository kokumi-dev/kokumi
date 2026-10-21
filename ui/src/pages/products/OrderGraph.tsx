import { Link } from 'react-router'
import type { Order, Preparation, Product, Serving } from '../../api/types'
import GraphView from '../../components/graph/GraphView'
import LayeredGraph, { type GraphEdge, type GraphNode } from '../../components/graph/LayeredGraph'
import { Chip, NodeCard, NodeIcon, NodeSummary, StaticCard, TagIcon, type Status } from '../../components/graph/parts'
import { useGraphSelection } from '../../components/graph/useGraphSelection'
import { paths } from '../../routes/paths'
import { productSourceRef } from '../../utils/products'
import { pendingLabel, pendingText } from '../../utils/promotion'
import OrderDetails from './OrderDetails'
import { OrderIcon, ProductIcon } from './orderIcons'
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

const productNode = 'product'
const newOrderNode = 'new-order'
const orderNode = (order: Order) => `order:${order.name}`

/**
 * OrderGraph draws a Product and its Orders from left to right, each Order
 * after the Orders it runs after, and the details of the Order selected
 * beside them. An Order without upstream stages follows the Product. A
 * Product without Orders is followed by a link to create the first; the page
 * header offers it otherwise.
 */
export default function OrderGraph({ product, orders, preparations, servings, orderDenied }: Props) {
  const { selected, toggle, clear } = useGraphSelection()
  const insights = orderInsights(orders ?? [], preparations, servings, product.revision)
  const current = insights.find((i) => orderNode(i.order) === selected)
  const names = new Set(insights.map((i) => i.order.name))
  const upstreamsOf = (order: Order) => (order.after ?? []).map((s) => s.order).filter((n) => names.has(n))

  const first = insights.length === 0
  const nodes: GraphNode[] = [
    { id: productNode, card: <ProductCard product={product} /> },
    ...insights.map((insight) => ({
      id: orderNode(insight.order),
      card: <OrderCard insight={insight} selected={insight === current} onSelect={() => toggle(orderNode(insight.order))} />,
    })),
    ...(first ? [{ id: newOrderNode, card: <FirstOrderCard product={product} denied={orderDenied} /> }] : []),
  ]
  const edges: GraphEdge[] = [
    ...insights.flatMap((insight) => {
      const upstreams = upstreamsOf(insight.order)
      return upstreams.length > 0
        ? upstreams.map((name) => ({ from: `order:${name}`, to: orderNode(insight.order) }))
        : [{ from: productNode, to: orderNode(insight.order) }]
    }),
    ...(first ? [{ from: productNode, to: newOrderNode, style: 'dashed' as const }] : []),
  ]

  return (
    <GraphView
      loading={orders === null}
      inspector={current && <OrderDetails key={current.order.name} insight={current} onClose={clear} />}
    >
      <LayeredGraph nodes={nodes} edges={edges} label={`${product.name} and its Orders`} />
    </GraphView>
  )
}

/** The Product the Orders are variants of: its state and the source it renders. */
function ProductCard({ product }: { product: Product }) {
  return (
    <StaticCard className={styles.product}>
      <span className={styles.row}>
        <NodeSummary icon={<ProductIcon />} title={product.name} status={stateStatus(product)} />
        {product.source.version && (
          <Chip icon={<TagIcon />} mono className={styles.trail} title="The version of the source">{product.source.version}</Chip>
        )}
      </span>
      <span className={styles.source} title={productSourceRef(product)}>{productSourceRef(product)}</span>
    </StaticCard>
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

/** An Order: its state, the version it runs and what its latest Preparation waits for. */
function OrderCard({ insight, selected, onSelect }: { insight: OrderInsight; selected: boolean; onSelect: () => void }) {
  const { order } = insight
  return (
    <NodeCard nodeKey={orderNode(order)} selected={selected} onClick={onSelect}>
      <NodeSummary
        icon={<OrderIcon muted={!insight.active} />}
        title={order.name}
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
