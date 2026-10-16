/** An edge of a directed graph, from the node that comes first. */
export interface Link {
  from: string
  to: string
}

/**
 * layers sorts the nodes of a directed graph into ranks for a layered
 * drawing: a node without incoming edges goes into the first rank, any other
 * one rank after the furthest node with an edge to it. An edge that closes a
 * cycle, or names a node that is not in ids, is ignored. Each rank keeps the
 * order of ids.
 */
export function layers(ids: string[], links: Link[]): string[][] {
  const known = new Set(ids)
  const incoming = new Map<string, string[]>()
  for (const { from, to } of links) {
    if (!known.has(from) || !known.has(to) || from === to) continue
    incoming.set(to, [...(incoming.get(to) ?? []), from])
  }

  const ranks = new Map<string, number>()
  const visiting = new Set<string>()
  const rankOf = (id: string): number => {
    const cached = ranks.get(id)
    if (cached !== undefined) return cached
    visiting.add(id)
    let rank = 0
    for (const from of incoming.get(id) ?? []) {
      if (!visiting.has(from)) rank = Math.max(rank, rankOf(from) + 1)
    }
    visiting.delete(id)
    ranks.set(id, rank)
    return rank
  }

  const result: string[][] = []
  for (const id of ids) (result[rankOf(id)] ??= []).push(id)
  return result.filter(Boolean)
}
