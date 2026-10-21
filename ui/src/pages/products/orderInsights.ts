import type { Order, Preparation, Serving } from '../../api/types'
import type { Status } from '../../components/graph/parts'
import { plural } from '../../utils/format'
import { pendingUpdate, type PendingUpdate } from '../../utils/promotion'
import { stateTone, type Readiness } from '../../utils/state'

/** What an Order currently runs and whether something newer waits. */
export interface OrderInsight {
  order: Order
  /** Name of the Preparation the Order's Serving runs. */
  active?: string
  /** Source version of the active Preparation, e.g. "6.14.1". */
  activeVersion?: string
  /** True while the Preparations that hold the active version load. */
  versionLoading: boolean
  /** Why the latest Preparation is not deployed yet; undefined when it is, or there is none. */
  pending?: PendingUpdate
  /** True when the active Preparation renders the Product revision its Orders render now. */
  currentRevision: boolean
  /** What the Order changes on its Product, e.g. "2 values · 1 patch". */
  overlay: string
}

/**
 * Derives the insight for each Order of one Product. What an Order runs
 * comes with the Order; only its version and revision need the
 * Preparations, and what holds its latest Preparation the Servings, which
 * may still load. revision is the Product revision its Orders render now.
 */
export function orderInsights(
  orders: Order[],
  preparations: Preparation[] | null,
  servings: Serving[] | null,
  revision?: string,
): OrderInsight[] {
  const find = (order: Order, name?: string) =>
    name ? preparations?.find((p) => p.namespace === order.namespace && p.name === name) : undefined

  return orders.map((order) => {
    const active = order.activePreparation
    const prep = find(order, active)

    return {
      order,
      active,
      activeVersion: prep ? versionOf(prep) : undefined,
      versionLoading: !!active && preparations === null,
      pending: pendingUpdate(order, find(order, order.latestRevision), orders, servings),
      currentRevision: !!revision && prep?.productRevision === revision,
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
export function stateStatus(object: Readiness): Status {
  return { tone: stateTone(object), label: object.state || 'Unknown' }
}
