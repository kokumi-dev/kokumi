import type { Order } from '../../api/types'

/** The Orders of a Product laid out by the Environments they deploy to: one column each, waves inside. */
export interface EnvironmentLayout {
  /** The Orders in the order they are drawn: by column, environment, wave and name. */
  orders: Order[]
  /** The column of each Order, by name, from 0. */
  column: Map<string, number>
  /** The title of each column: the Environments in it. */
  titles: string[]
  /** The wave of each Order inside an Environment of several waves, from 1, with the Orders it runs after there. */
  wave: Map<string, { wave: number; after: string[] }>
  /** Whether the link from one Order to another stays inside an Environment, where its wave shows it. */
  inside: (from: string, to: string) => boolean
}

/** Why the Environments of the Orders cannot be drawn as columns. */
export interface EnvironmentConflict {
  conflict: string
}

/**
 * environmentLayout groups the Orders of a Product by the Environment they
 * deploy to, and puts each Environment one column after the furthest
 * Environment it runs after. In an Environment, an Order is one wave after
 * the furthest Order it runs after there. Undefined without Orders. A
 * conflict when an Environment runs after itself through another, so that no
 * order of columns follows the stages.
 */
export function environmentLayout(orders: Order[]): EnvironmentLayout | EnvironmentConflict | undefined {
  if (orders.length === 0) return undefined

  const byName = new Map(orders.map((o) => [o.name, o]))
  const group = (o: Order) => o.environmentRef.name
  const upstreams = (o: Order) => (o.after ?? []).map((s) => byName.get(s.order)).filter((u): u is Order => !!u)

  const groupsBefore = new Map<string, Set<string>>()
  for (const o of orders) {
    const before = groupsBefore.get(group(o)) ?? new Set<string>()
    for (const u of upstreams(o)) if (group(u) !== group(o)) before.add(group(u))
    groupsBefore.set(group(o), before)
  }

  const columnOf = new Map<string, number>()
  const visiting: string[] = []
  const columnOfGroup = (g: string): number | string[] => {
    const cached = columnOf.get(g)
    if (cached !== undefined) return cached
    if (visiting.includes(g)) return [...visiting.slice(visiting.indexOf(g)), g]
    visiting.push(g)
    let column = 0
    for (const before of groupsBefore.get(g) ?? []) {
      const c = columnOfGroup(before)
      if (Array.isArray(c)) return c
      column = Math.max(column, c + 1)
    }
    visiting.pop()
    columnOf.set(g, column)
    return column
  }
  for (const g of groupsBefore.keys()) {
    const c = columnOfGroup(g)
    if (Array.isArray(c)) return { conflict: `The Environments run after each other in a circle: ${c.join(' → ')}.` }
  }

  const waveOf = new Map<string, number>()
  const wave = (o: Order, seen: Set<string>): number => {
    const cached = waveOf.get(o.name)
    if (cached !== undefined) return cached
    seen.add(o.name)
    let w = 1
    for (const u of upstreams(o)) {
      if (group(u) === group(o) && !seen.has(u.name)) w = Math.max(w, wave(u, seen) + 1)
    }
    seen.delete(o.name)
    waveOf.set(o.name, w)
    return w
  }
  for (const o of orders) wave(o, new Set())

  const waves = new Map<string, number>()
  for (const o of orders) waves.set(group(o), Math.max(waves.get(group(o)) ?? 0, waveOf.get(o.name)!))

  const sorted = [...orders].sort(
    (a, b) =>
      columnOf.get(group(a))! - columnOf.get(group(b))! ||
      group(a).localeCompare(group(b)) ||
      waveOf.get(a.name)! - waveOf.get(b.name)! ||
      a.name.localeCompare(b.name),
  )

  const titles: string[] = []
  for (const o of sorted) {
    const c = columnOf.get(group(o))!
    const environments = new Set((titles[c] ?? '').split(' · ').filter(Boolean))
    environments.add(group(o))
    titles[c] = [...environments].join(' · ')
  }

  return {
    orders: sorted,
    column: new Map(orders.map((o) => [o.name, columnOf.get(group(o))!])),
    titles: Array.from(titles, (t) => t ?? ''),
    wave: new Map(
      orders
        .filter((o) => waves.get(group(o))! > 1)
        .map((o) => [
          o.name,
          { wave: waveOf.get(o.name)!, after: upstreams(o).filter((u) => group(u) === group(o)).map((u) => u.name) },
        ]),
    ),
    inside: (from, to) => {
      const a = byName.get(from)
      const b = byName.get(to)
      return !!a && !!b && group(a) === group(b)
    },
  }
}
