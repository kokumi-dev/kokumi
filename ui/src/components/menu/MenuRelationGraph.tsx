import { useMemo } from 'react'
import {
  ReactFlow,
  Background,
  type Edge,
  type Node,
  type NodeProps,
  Handle,
  Position,
  type NodeMouseHandler,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import type { Menu, Order } from '../../api/types'
import styles from './MenuRelationGraph.module.css'

interface Props {
  menu: Menu
  orders: Order[] | null
  onSelectOrder: (order: Order) => void
}

type GraphNodeData = {
  kind: 'menu' | 'order'
  name: string
  source?: string
  state?: string
  ready?: string
  order?: Order
}

type GraphNode = Node<GraphNodeData>

function MenuNode({ data }: NodeProps<GraphNode>) {
  return (
    <div className={styles.menuNode}>
      <Handle type="source" position={Position.Right} />
      <span className={styles.menuNodeLabel}>Menu</span>
      <div className={styles.menuNodeName}>{data.name}</div>
      {data.source && <div className={styles.menuNodeSource}>{data.source}</div>}
    </div>
  )
}

function OrderNode({ data }: NodeProps<GraphNode>) {
  const dotClass =
    data.ready === 'True'
      ? styles.dotReady
      : data.ready === 'False'
        ? styles.dotNotReady
        : styles.dotUnknown
  const borderClass =
    data.ready === 'True'
      ? styles.orderNodeReady
      : data.ready === 'False'
        ? styles.orderNodeNotReady
        : styles.orderNodeUnknown
  return (
    <div className={`${styles.orderNode} ${borderClass}`}>
      <Handle type="target" position={Position.Left} />
      <span className={styles.orderNodeLabel}>Order</span>
      <div className={styles.orderNodeName}>
        <span className={`${styles.dot} ${dotClass}`} />
        {data.name}
      </div>
      {data.state && <div className={styles.orderNodeState}>{data.state}</div>}
    </div>
  )
}

const nodeTypes = { menu: MenuNode, order: OrderNode }

const MENU_X = 0
const ORDER_X = 360
const ORDER_Y_STEP = 110

function readyStatus(order: Order): string | undefined {
  return order.conditions?.find((c) => c.type === 'Ready')?.status
}

export default function MenuRelationGraph({ menu, orders, onSelectOrder }: Props) {
  const onNodeClick: NodeMouseHandler<GraphNode> = (_, node) => {
    if (node.type === 'order' && node.data.order) onSelectOrder(node.data.order)
  }

  const variants = useMemo(
    () =>
      (orders ?? []).filter(
        (o) => o.menuRef?.name === menu.name && o.namespace === menu.namespace,
      ),
    [orders, menu.name, menu.namespace],
  )

  const source = menu.source.pantryRef?.name
    ? `pantry: ${menu.source.pantryRef.name}`
    : menu.source.oci

  const { nodes, edges } = useMemo(() => {
    const rawNodes: GraphNode[] = [
      {
        id: `menu:${menu.namespace}:${menu.name}`,
        type: 'menu',
        data: {
          kind: 'menu',
          name: menu.name,
          source,
        },
        position: {
          x: MENU_X,
          // Same node heights -> centering the Menu node on the middle
          // Order's center also aligns the edge handles.
          y: ((variants.length - 1) * ORDER_Y_STEP) / 2,
        },
      },
      ...variants.map<GraphNode>((o, i) => ({
        id: `order:${o.namespace}:${o.name}`,
        type: 'order',
        data: {
          kind: 'order',
          name: o.name,
          state: o.state,
          ready: readyStatus(o),
          order: o,
        },
        position: { x: ORDER_X, y: i * ORDER_Y_STEP },
      })),
    ]
    const rawEdges: Edge[] = variants.map((o) => ({
      id: `e:${o.namespace}:${o.name}`,
      source: `menu:${menu.namespace}:${menu.name}`,
      target: `order:${o.namespace}:${o.name}`,
      type: 'smoothstep',
      animated: false,
      markerEnd: { type: 'arrowclosed' },
    }))
    return { nodes: rawNodes, edges: rawEdges }
  }, [menu.namespace, menu.name, source, variants])

  if (variants.length === 0) {
    return (
      <div className={styles.empty}>
        <span className={styles.emptyTitle}>No Orders yet</span>
        <span className={styles.emptyHint}>
          Orders referencing this Menu will appear here.
        </span>
      </div>
    )
  }

  return (
    <div className={styles.graph}>
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        onNodeClick={onNodeClick}
        fitView
        fitViewOptions={{ padding: 0.15 }}
        minZoom={0.5}
        proOptions={{ hideAttribution: true }}
        nodesDraggable={false}
        nodesConnectable={false}
        elementsSelectable={false}
      >
        <Background gap={16} />
      </ReactFlow>
    </div>
  )
}
