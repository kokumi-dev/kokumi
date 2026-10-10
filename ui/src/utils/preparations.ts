import type { Preparation } from '../api/types'

/**
 * Sorts preparations latest-to-oldest by following parentDigest links backwards
 * from the newest tip (an entry whose digest no other entry claims as a parent).
 * Preparations outside that chain follow, newest first.
 */
export function sortByChain(preps: Preparation[]): Preparation[] {
  if (preps.length <= 1) return preps
  const byDigest = new Map(preps.map((p) => [p.artifact.digest, p]))
  const parentDigests = new Set(preps.map((p) => p.parentDigest).filter(Boolean))
  const tip = preps
    .filter((p) => !parentDigests.has(p.artifact.digest))
    .sort((a, b) => (b.createdAt ?? '').localeCompare(a.createdAt ?? ''))[0]
  if (!tip) return preps
  const ordered: Preparation[] = []
  let cur: Preparation | undefined = tip
  while (cur) {
    ordered.push(cur)
    cur = cur.parentDigest ? byDigest.get(cur.parentDigest) : undefined
  }
  const inChain = new Set(ordered)
  const rest = preps
    .filter((p) => !inChain.has(p))
    .sort((a, b) => (b.createdAt ?? '').localeCompare(a.createdAt ?? ''))
  return [...ordered, ...rest]
}

/** Returns the Preparations of the given Order in the given namespace. */
export function preparationsOf(all: Preparation[] | null, namespace: string, order: string): Preparation[] | null {
  if (all === null) return null
  return all.filter((p) => p.namespace === namespace && p.order === order)
}

/**
 * Returns "Rollback" when the preparation is older than the currently active
 * one, and "Promote" otherwise (including when there is no active or the dates
 * cannot be compared).
 */
export function promoteLabel(prepCreatedAt?: string, activeCreatedAt?: string): 'Promote' | 'Rollback' {
  if (!prepCreatedAt || !activeCreatedAt) return 'Promote'
  return new Date(prepCreatedAt) < new Date(activeCreatedAt) ? 'Rollback' : 'Promote'
}

export function approvalLabel(state: string, approved: number, required: number): string {
  switch (state) {
    case 'Approved':
      return 'Approved'
    case 'ChangesRequested':
      return 'Changes requested'
    default:
      return `${approved}/${required} approvals`
  }
}
