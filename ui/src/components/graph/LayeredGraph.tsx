import { useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { layers, type Link } from './layers'
import styles from './LayeredGraph.module.css'

/** How an edge is drawn: solid, or dashed or dotted for a looser tie. */
export type EdgeStyle = 'solid' | 'dashed' | 'dotted'

export interface GraphNode {
  id: string
  card: ReactNode
}

export interface GraphEdge extends Link {
  style?: EdgeStyle
}

interface Props {
  nodes: GraphNode[]
  edges: GraphEdge[]
  /** Names the graph for assistive technology. */
  label?: string
}

/** Space between ranks; edges turn in the middle of it. */
const GAP = 56
/** Radius of the corners of an edge, as of the elbows of a tree. */
const RADIUS = 8
/** Length and half the width of an arrowhead. */
const ARROW = 7
const ARROW_HALF = 4.75

interface DrawnEdge {
  key: string
  style: EdgeStyle
  line: string
  arrow: string
}

/**
 * The path of an edge from the right of box a to the left of box b, both
 * relative to origin. It turns in the gap before b, with rounded corners,
 * and ends in an arrowhead.
 */
function route(a: DOMRect, b: DOMRect, origin: DOMRect): Pick<DrawnEdge, 'line' | 'arrow'> {
  const x1 = a.right - origin.left
  const y1 = a.top + a.height / 2 - origin.top
  const x2 = b.left - origin.left
  const y2 = b.top + b.height / 2 - origin.top
  const end = x2 - ARROW
  const arrow = `M${x2} ${y2}L${end} ${y2 - ARROW_HALF}L${end} ${y2 + ARROW_HALF}Z`
  const dy = y2 - y1
  if (Math.abs(dy) < 1) return { line: `M${x1} ${y1}H${end}`, arrow }

  const mid = x2 - GAP / 2
  const r = Math.min(RADIUS, Math.abs(dy) / 2)
  const s = Math.sign(dy)
  return {
    line: `M${x1} ${y1}H${mid - r}Q${mid} ${y1} ${mid} ${y1 + s * r}V${y2 - s * r}Q${mid} ${y2} ${mid + r} ${y2}H${end}`,
    arrow,
  }
}

/**
 * LayeredGraph draws a directed acyclic graph from left to right: each node
 * one rank after the furthest node with an edge to it, the ranks side by
 * side, and the edges between the cards. The cards are laid out by CSS; the
 * edges follow them as they are measured.
 */
export default function LayeredGraph({ nodes, edges, label }: Props) {
  const ref = useRef<HTMLDivElement>(null)
  const [drawn, setDrawn] = useState<DrawnEdge[]>([])
  const ranks = layers(nodes.map((n) => n.id), edges)
  const cards = new Map(nodes.map((n) => [n.id, n.card]))

  useLayoutEffect(() => {
    const graph = ref.current
    if (!graph) return
    const measure = () => {
      const origin = graph.getBoundingClientRect()
      const boxes = new Map<string, DOMRect>()
      for (const el of graph.querySelectorAll<HTMLElement>('[data-layer-node]')) {
        boxes.set(el.dataset.layerNode ?? '', el.getBoundingClientRect())
      }
      const next = edges.flatMap((e) => {
        const a = boxes.get(e.from)
        const b = boxes.get(e.to)
        return a && b ? [{ key: `${e.from}>${e.to}`, style: e.style ?? 'solid', ...route(a, b, origin) }] : []
      })
      setDrawn((prev) => (JSON.stringify(prev) === JSON.stringify(next) ? prev : next))
    }
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(graph)
    for (const el of graph.querySelectorAll('[data-layer-node]')) observer.observe(el)
    return () => observer.disconnect()
  }, [nodes, edges])

  return (
    <div ref={ref} className={styles.graph} style={{ columnGap: GAP }} role="group" aria-label={label}>
      <svg className={styles.edges} aria-hidden>
        {drawn.map((e) => (
          <g key={e.key} className={styles[e.style]}>
            <path className={styles.line} d={e.line} />
            <path className={styles.arrow} d={e.arrow} />
          </g>
        ))}
      </svg>
      {ranks.map((ids, i) => (
        <ul key={i} className={styles.rank}>
          {ids.map((id) => (
            <li key={id} className={styles.node} data-layer-node={id}>{cards.get(id)}</li>
          ))}
        </ul>
      ))}
    </div>
  )
}
