import { useState } from 'react'
import { promote } from '../../api/client'
import type { Order, Preparation } from '../../api/types'
import { useOrders } from '../../hooks/useOrders'
import { usePreparations } from '../../hooks/usePreparations'
import { formatDate, shortDigest } from '../../utils/format'
import DiffModal from '../preparation/DiffModal'
import ManifestModal from '../preparation/ManifestModal'
import Btn from '../shared/Btn'
import pageStyles from '../../pages/pages.module.css'
import styles from './OpenPromotions.module.css'

// ── Types ────────────────────────────────────────────────────────────────────

/** Order that has a latestRevision not yet matching its activePreparation. */
type PromotableOrder = Order & { latestRevision: string }

// ── OpenPromotions ────────────────────────────────────────────────────────────

interface DiffTarget {
  prep: Preparation
  active: Preparation
}

export default function OpenPromotions() {
  const orders = useOrders()
  const preparations = usePreparations()

  const [manifestPrep, setManifestPrep] = useState<Preparation | null>(null)
  const [diffTarget, setDiffTarget] = useState<DiffTarget | null>(null)
  const [promotingKeys, setPromotingKeys] = useState<Set<string>>(new Set())

  if (orders === null || preparations === null) {
    return <p className={styles.loading}>Loading…</p>
  }

  // Build a fast lookup: "namespace/name" → Preparation
  const prepByKey = new Map<string, Preparation>()
  for (const p of preparations) {
    prepByKey.set(`${p.namespace}/${p.name}`, p)
  }

  // Orders that have a latestRevision which is not yet the activePreparation
  const promotable = orders
    .filter((o): o is PromotableOrder =>
      !!o.latestRevision &&
      o.phase === 'Ready' &&
      o.latestRevision !== o.activePreparation,
    )
    .sort((a, b) => {
      const aDate = a.latestRevision ? prepByKey.get(`${a.namespace}/${a.latestRevision}`)?.createdAt : undefined
      const bDate = b.latestRevision ? prepByKey.get(`${b.namespace}/${b.latestRevision}`)?.createdAt : undefined
      if (!aDate && !bDate) return 0
      if (!aDate) return 1
      if (!bDate) return -1
      return new Date(bDate).getTime() - new Date(aDate).getTime()
    })

  if (promotable.length === 0) {
    return (
      <div className={styles.emptyState}>
        <svg
          className={styles.emptyIcon}
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.5"
          strokeLinecap="round"
          strokeLinejoin="round"
        >
          <path d="M20 6L9 17l-5-5" />
        </svg>
        <span className={styles.emptyText}>All orders are up to date</span>
      </div>
    )
  }

  async function handlePromote(order: PromotableOrder) {
    const key = `${order.namespace}/${order.name}`
    setPromotingKeys((prev) => new Set(prev).add(key))
    try {
      await promote(order.namespace, order.name, order.latestRevision)
    } finally {
      setPromotingKeys((prev) => {
        const next = new Set(prev)
        next.delete(key)
        return next
      })
    }
  }

  return (
    <>
      <table className={pageStyles.table}>
        <thead className={pageStyles.tableHead}>
          <tr>
            <th>Order</th>
            <th>Namespace</th>
            <th>Commit Message</th>
            <th>From</th>
            <th>To</th>
            <th>Created</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          {promotable.map((order) => {
            const latestPrep = order.latestRevision
              ? prepByKey.get(`${order.namespace}/${order.latestRevision}`)
              : undefined
            const activePrep = order.activePreparation
              ? prepByKey.get(`${order.namespace}/${order.activePreparation}`)
              : undefined
            const canDiff = !!activePrep && !!latestPrep
            const key = `${order.namespace}/${order.name}`
            const promoting = promotingKeys.has(key)

            return (
              <tr key={key} className={pageStyles.tableRow}>
                <td>{order.name}</td>
                <td>{order.namespace}</td>
                <td className={styles.commitMessage}>
                  {latestPrep?.commitMessage?.trim() || <span className={styles.muted}>—</span>}
                </td>
                <td>
                  {activePrep
                    ? <span className={styles.hashChip}>{shortDigest(activePrep.artifact.digest)}</span>
                    : <span className={styles.muted}>(new)</span>}
                </td>
                <td>
                  {latestPrep
                    ? <span className={styles.hashChip}>{shortDigest(latestPrep.artifact.digest)}</span>
                    : <span className={styles.muted}>—</span>}
                </td>
                <td style={{ whiteSpace: 'nowrap' }}>{formatDate(latestPrep?.createdAt)}</td>
                <td>
                  <div className={styles.actions}>
                    {canDiff && (
                      <Btn
                        variant="ghost"
                        size="sm"
                        onClick={() => {
                          if (latestPrep && activePrep) {
                            setDiffTarget({ prep: latestPrep, active: activePrep })
                          }
                        }}
                      >
                        Diff
                      </Btn>
                    )}
                    <Btn
                      variant="ghost"
                      size="sm"
                      onClick={() => latestPrep && setManifestPrep(latestPrep)}
                      disabled={!latestPrep}
                    >
                      Manifest
                    </Btn>
                    <Btn
                      variant="promote"
                      size="sm"
                      onClick={() => handlePromote(order)}
                      disabled={promoting}
                    >
                      {promoting ? '…' : 'Promote'}
                    </Btn>
                  </div>
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>

      {manifestPrep && (
        <ManifestModal
          preparation={manifestPrep}
          onClose={() => setManifestPrep(null)}
        />
      )}

      {diffTarget && (
        <DiffModal
          preparation={diffTarget.prep}
          activePreparation={diffTarget.active}
          onClose={() => setDiffTarget(null)}
        />
      )}
    </>
  )
}
