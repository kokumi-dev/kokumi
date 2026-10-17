import { useEffect, useState } from 'react'
import { getManifest, previewOrder } from '../../api/client'
import type { Order, OrderPreviewRequest } from '../../api/types'
import { computeDiff, filterContext } from '../../utils/diff'
import { filterCRDDocuments, hasCRDDocuments, joinFiles } from '../../utils/manifest'
import { Button } from '../ui'
import DiffView from '../shared/DiffView'
import styles from './DiffTab.module.css'

interface Props {
  request: OrderPreviewRequest
  order: Order
}

const DEBOUNCE_MS = 600
const CONTEXT_SIZE = 5

/** Diffs the active Preparation's manifest against a preview of the edited Order. */
export default function DiffTab({ request, order }: Props) {
  const [loading, setLoading] = useState(false)
  const [before, setBefore] = useState<string | null>(null)
  const [after, setAfter] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [showFull, setShowFull] = useState(false)
  const [hideCRDs, setHideCRDs] = useState(true)

  const activePrepName = order.activePreparation ?? ''
  // Keyed by content, so the caller may build a fresh request on every render.
  const key = JSON.stringify(request)

  useEffect(() => {
    if (!activePrepName) return
    const req = JSON.parse(key) as OrderPreviewRequest
    let cancelled = false
    const timer = setTimeout(() => {
      setLoading(true)
      setError(null)
      Promise.all([
        getManifest(order.namespace, activePrepName),
        previewOrder(req),
      ])
        .then(([active, preview]) => {
          if (cancelled) return
          setBefore(joinFiles(active))
          setAfter(joinFiles(preview))
          setLoading(false)
        })
        .catch((e: Error) => {
          if (cancelled) return
          setError(e.message)
          setLoading(false)
        })
    }, DEBOUNCE_MS)

    return () => {
      cancelled = true
      clearTimeout(timer)
    }
  }, [key, order.namespace, activePrepName])

  if (loading) {
    return <div className={styles.placeholder}>Loading diff…</div>
  }

  if (error) {
    return <p className={styles.error}>Failed to load diff: {error}</p>
  }

  if (before === null || after === null) {
    return null
  }

  const hasCRDs = hasCRDDocuments(before) || hasCRDDocuments(after)
  const b = filterCRDDocuments(before, hideCRDs)
  const a = filterCRDDocuments(after, hideCRDs)
  const allLines = computeDiff(b, a)
  const displayLines = filterContext(allLines, showFull ? Infinity : CONTEXT_SIZE)

  return (
    <div>
      <div className={styles.toolbar}>
        <span className={styles.toolbarLabel}>
          {activePrepName} (active) → preview
        </span>
        <div style={{ display: 'flex', gap: '8px' }}>
          {hasCRDs && (
            <Button variant="secondary" size="sm" onClick={() => setHideCRDs((v) => !v)}>
              {hideCRDs ? 'Show CRDs' : 'Hide CRDs'}
            </Button>
          )}
          <Button variant="secondary" size="sm" onClick={() => setShowFull((v) => !v)}>
            {showFull ? 'Show changed only' : 'Show full file'}
          </Button>
        </div>
      </div>
      <DiffView lines={displayLines} />
    </div>
  )
}
