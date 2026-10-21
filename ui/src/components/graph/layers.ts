/** An edge of a directed graph, from the node that comes first. */
export interface Link {
  from: string
  to: string
}

/**
 * A slot of a rank: a node, or the lane of a link that crosses the rank.
 * The id of a node is its id; that of a lane names its link and rank, apart
 * from the node ids.
 */
export interface Slot {
  kind: 'node' | 'lane'
  id: string
}

/** A layered drawing of a directed graph, as layers lays it out. */
export interface Layers {
  /** The ranks from left to right, each from top to bottom. */
  ranks: Slot[][]
  /**
   * For each link, the slots it runs through from its first node to its
   * last: the two nodes and a lane in each rank between them. Empty for a
   * link that is ignored.
   */
  paths: Slot[][]
}

/** A key that tells slots of both kinds apart. */
const keyOf = (slot: Slot) => `${slot.kind}:${slot.id}`

/** How often the ranks are reordered, alternately from the left and from the right. */
const SWEEPS = 4

/**
 * layers lays out a directed graph in ranks for a layered drawing, in the
 * steps of Sugiyama's method:
 *
 * 1. A node without incoming links goes into the first rank, any other one
 *    rank after the furthest node with a link to it. A link that closes a
 *    cycle, or names a node that is not in ids, is ignored.
 * 2. A link that spans several ranks gets a lane in each rank it crosses,
 *    so that its edge runs beside the nodes there instead of through them.
 * 3. Each rank is ordered by the barycenter of its neighbours in the rank
 *    before it, and then in the rank after it, a few times over, so that
 *    edges run straight and seldom cross. Ties keep the order of ids, and
 *    lanes come after the nodes.
 */
export function layers(ids: string[], links: Link[]): Layers {
  const known = new Set(ids)
  const valid = (l: Link) => known.has(l.from) && known.has(l.to) && l.from !== l.to
  const incoming = new Map<string, string[]>()
  for (const l of links) {
    if (valid(l)) incoming.set(l.to, [...(incoming.get(l.to) ?? []), l.from])
  }

  const rankOf = new Map<string, number>()
  const visiting = new Set<string>()
  const rank = (id: string): number => {
    const cached = rankOf.get(id)
    if (cached !== undefined) return cached
    visiting.add(id)
    let r = 0
    for (const from of incoming.get(id) ?? []) {
      if (!visiting.has(from)) r = Math.max(r, rank(from) + 1)
    }
    visiting.delete(id)
    rankOf.set(id, r)
    return r
  }

  const ranks: Slot[][] = []
  for (const id of ids) (ranks[rank(id)] ??= []).push({ kind: 'node', id })

  // Split each link into one hop per rank, through lanes.
  const before = new Map<string, string[]>()
  const after = new Map<string, string[]>()
  const hop = (a: Slot, b: Slot) => {
    after.set(keyOf(a), [...(after.get(keyOf(a)) ?? []), keyOf(b)])
    before.set(keyOf(b), [...(before.get(keyOf(b)) ?? []), keyOf(a)])
  }
  const paths = links.map((l, i): Slot[] => {
    if (!valid(l)) return []
    const from = rank(l.from)
    const to = rank(l.to)
    const first: Slot = { kind: 'node', id: l.from }
    const last: Slot = { kind: 'node', id: l.to }
    // A link that closes a cycle points backwards; it is drawn as it is.
    if (to <= from) return [first, last]
    const path = [first]
    for (let r = from + 1; r < to; r++) {
      const lane: Slot = { kind: 'lane', id: `${i}:${r}` }
      ranks[r].push(lane)
      path.push(lane)
    }
    path.push(last)
    for (let k = 1; k < path.length; k++) hop(path[k - 1], path[k])
    return path
  })

  const filled = ranks.filter(Boolean)
  for (let sweep = 0; sweep < SWEEPS; sweep++) {
    const down = sweep % 2 === 0
    const order = down ? filled.map((_, r) => r).slice(1) : filled.map((_, r) => r).reverse().slice(1)
    for (const r of order) {
      const reference = filled[down ? r - 1 : r + 1]
      const position = new Map(reference.map((slot, k) => [keyOf(slot), k]))
      const neighbours = down ? before : after
      const barycenter = new Map(
        filled[r].map((slot, k) => {
          const at = (neighbours.get(keyOf(slot)) ?? []).flatMap((n) => position.get(n) ?? [])
          return [slot, at.length ? at.reduce((sum, p) => sum + p, 0) / at.length : k] as const
        }),
      )
      const isLane = (slot: Slot) => Number(slot.kind === 'lane')
      filled[r] = [...filled[r]].sort((a, b) => barycenter.get(a)! - barycenter.get(b)! || isLane(a) - isLane(b))
    }
  }

  return { ranks: filled, paths }
}
