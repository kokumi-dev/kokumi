import type { Order, Preparation } from '../../api/types'
import type { Status } from '../../components/graph/parts'
import { plural } from '../../utils/format'

/** What an Order currently runs and whether something newer waits. */
export interface OrderInsight {
  order: Order
  /** Name of the Preparation the Order's Serving runs. */
  active?: string
  /** Source version of the active Preparation, e.g. "6.14.1". */
  activeVersion?: string
  /** True while the Preparations that hold the active version load. */
  versionLoading: boolean
  /** True when the latest Preparation is not the one being served. */
  updateReady: boolean
  /** What the Order changes on its Product, e.g. "2 values · 1 patch". */
  overlay: string
}

/**
 * Derives the insight for each Order. What an Order runs comes with the
 * Order; only its version needs the Preparations, which may still load.
 */
export function orderInsights(orders: Order[], preparations: Preparation[] | null): OrderInsight[] {
  return orders.map((order) => {
    const active = order.activePreparation
    const prep = active
      ? preparations?.find((p) => p.namespace === order.namespace && p.name === active)
      : undefined

    return {
      order,
      active,
      activeVersion: prep ? versionOf(prep) : undefined,
      versionLoading: !!active && preparations === null,
      updateReady: !!order.latestRevision && !!active && order.latestRevision !== active,
      overlay: overlaySummary(order),
    }
  })
}

/** Source version of a Preparation, read from its artifact reference. */
function versionOf(prep: Preparation): string | undefined {
  return /:([^:@/]+)@sha256:/.exec(prep.artifact.ociRef)?.[1]
}

function overlaySummary(order: Order): string {
  const values = countLeaves(order.values)
  const patches = order.patches?.length ?? 0
  const edits = order.edits?.length ?? 0
  const parts = [
    values && plural(values, 'value'),
    patches && plural(patches, 'patch'),
    edits && plural(edits, 'edit'),
  ].filter(Boolean)
  return parts.length ? parts.join(' · ') : 'Same as Product'
}

function countLeaves(value: unknown): number {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return value === undefined ? 0 : 1
  return Object.values(value).reduce<number>((n, v) => n + countLeaves(v), 0)
}

/** The status mark of the state of a Product or an Order. */
export function stateStatus(state?: string): Status {
  const label = state || 'Unknown'
  switch (label) {
    case 'Ready':
    case 'Resolved':
      return { tone: 'ok', label }
    case 'Failed':
      return { tone: 'bad', label }
    default:
      return { tone: 'neutral', label }
  }
}
