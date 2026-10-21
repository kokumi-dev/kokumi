import { useLayoutEffect, useRef, useState, type CSSProperties, type ReactNode } from 'react'
import { layers, type Link, type Slot } from './layers'
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
  /** Places each node in this rank instead of after the nodes with an edge to it; see LayerOptions. */
  rank?: (id: string) => number
  /** A title above each rank, e.g. the group it shows; empty for none. */
  titles?: string[]
  /** Names the graph for assistive technology. */
  label?: string
}

/** Space between ranks; edges turn in the middle of it. */
const GAP = 36
/** Space between the cards and lanes of a rank. */
const ROW_GAP = 12
/** Space above the ranks for their titles. */
const TITLE_SPACE = 30
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
 * The path of an edge through boxes, relative to origin: from the right of
 * each box to the left of the next, and across the lanes between the first
 * box and the last. Each hop turns in the gap before the next box,
 * with rounded corners; the edge ends in an arrowhead.
 */
function route(boxes: DOMRect[], origin: DOMRect): Pick<DrawnEdge, 'line' | 'arrow'> {
  const middle = (box: DOMRect) => box.top + box.height / 2 - origin.top
  let line = ''
  let arrow = ''
  for (let i = 1; i < boxes.length; i++) {
    const x1 = boxes[i - 1].right - origin.left
    const y1 = middle(boxes[i - 1])
    const x2 = boxes[i].left - origin.left
    const y2 = middle(boxes[i])
    const last = i === boxes.length - 1
    const end = last ? x2 - ARROW : x2
    // The first hop starts the path; a later one crosses the lane before it.
    line += i === 1 ? `M${x1} ${y1}` : `H${x1}`
    if (last) arrow = `M${x2} ${y2}L${end} ${y2 - ARROW_HALF}L${end} ${y2 + ARROW_HALF}Z`

    const dy = y2 - y1
    if (Math.abs(dy) < 1) {
      line += `H${end}`
      continue
    }
    const mid = x2 - GAP / 2
    const r = Math.min(RADIUS, Math.abs(dy) / 2)
    const s = Math.sign(dy)
    line += `H${mid - r}Q${mid} ${y1} ${mid} ${y1 + s * r}V${y2 - s * r}Q${mid} ${y2} ${mid + r} ${y2}H${end}`
  }
  return { line, arrow }
}

/**
 * The rows of a rank: 1fr for a card, none for a lane. Lanes above or below
 * the cards are matched with padding on the other side, so that the cards
 * stay centred on the rank and a lone card on the cards of the ranks beside
 * it.
 */
function rankStyle(slots: Slot[]): CSSProperties {
  const above = slots.findIndex((s) => s.kind === 'node')
  const below = [...slots].reverse().findIndex((s) => s.kind === 'node')
  return {
    gridTemplateRows: slots.map((s) => (s.kind === 'lane' ? '0' : '1fr')).join(' '),
    rowGap: ROW_GAP,
    paddingTop: Math.max(0, below - above) * ROW_GAP,
    paddingBottom: Math.max(0, above - below) * ROW_GAP,
  }
}

/**
 * LayeredGraph draws a directed acyclic graph from left to right: each node
 * one rank after the furthest node with an edge to it, the ranks side by
 * side, and the edges between the cards. An edge that spans several ranks
 * runs through a lane of its own in each rank it crosses. The cards and
 * lanes are laid out by CSS; the edges follow them as they are measured.
 */
export default function LayeredGraph({ nodes, edges, rank, titles, label }: Props) {
  const ref = useRef<HTMLDivElement>(null)
  const [drawn, setDrawn] = useState<DrawnEdge[]>([])
  const { ranks, paths } = layers(nodes.map((n) => n.id), edges, { rank })
  const titled = !!titles?.some(Boolean)
  const cards = new Map(nodes.map((n) => [n.id, n.card]))

  useLayoutEffect(() => {
    const graph = ref.current
    if (!graph) return
    const measure = () => {
      const origin = graph.getBoundingClientRect()
      const boxes = { node: new Map<string, DOMRect>(), lane: new Map<string, DOMRect>() }
      for (const el of graph.querySelectorAll<HTMLElement>('[data-layer-node]')) {
        boxes.node.set(el.dataset.layerNode ?? '', el.getBoundingClientRect())
      }
      for (const el of graph.querySelectorAll<HTMLElement>('[data-layer-lane]')) {
        boxes.lane.set(el.dataset.layerLane ?? '', el.getBoundingClientRect())
      }
      const next = edges.flatMap((e, i) => {
        const chain = paths[i].map((slot) => boxes[slot.kind].get(slot.id))
        if (chain.length < 2 || chain.some((box) => !box)) return []
        return [{ key: `${e.from}>${e.to}`, style: e.style ?? 'solid', ...route(chain as DOMRect[], origin) }]
      })
      setDrawn((prev) => (JSON.stringify(prev) === JSON.stringify(next) ? prev : next))
    }
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(graph)
    for (const el of graph.querySelectorAll('[data-layer-node]')) observer.observe(el)
    return () => observer.disconnect()
  }, [nodes, edges, paths])

  return (
    <div ref={ref} className={styles.graph} style={{ columnGap: GAP, paddingTop: titled ? TITLE_SPACE : undefined }} role="group" aria-label={label}>
      <svg className={styles.edges} aria-hidden>
        {drawn.map((e) => (
          <g key={e.key} className={styles[e.style]}>
            <path className={styles.line} d={e.line} />
            <path className={styles.arrow} d={e.arrow} />
          </g>
        ))}
      </svg>
      {ranks.map((slots, i) => (
        <div key={i} className={styles.column}>
          {titles?.[i] && <span className={styles.title}>{titles[i]}</span>}
          <ul className={styles.rank} style={rankStyle(slots)}>
            {slots.map((slot) =>
              slot.kind === 'lane' ? (
                <li key={`lane:${slot.id}`} className={styles.lane} data-layer-lane={slot.id} aria-hidden />
              ) : (
                <li key={slot.id} className={styles.node} data-layer-node={slot.id}>{cards.get(slot.id)}</li>
              ),
            )}
          </ul>
        </div>
      ))}
    </div>
  )
}
