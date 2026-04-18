import { useEffect, useRef, useState } from 'react'
import { getManifest, previewOrder } from '../../api/client'
import type { Order, OrderFormData } from '../../api/types'
import { computeDiff, filterContext } from '../../utils/diff'
import { filterCRDDocuments, hasCRDDocuments } from '../../utils/manifest'
import Btn from '../shared/Btn'
import DiffView from './DiffView'
import styles from './DiffTab.module.css'

interface Props {
  formData: OrderFormData
  order: Order
}

const DEBOUNCE_MS = 600
const CONTEXT_SIZE = 5

export default function DiffTab({ formData, order }: Props) {
  const [loading, setLoading] = useState(false)
  const [before, setBefore] = useState<string | null>(null)
  const [after, setAfter] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [showFull, setShowFull] = useState(false)
  const [hideCRDs, setHideCRDs] = useState(true)

  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  const activePrepName = order.activePreparation ?? ''

  useEffect(() => {
    if (!activePrepName) return

    if (timerRef.current !== null) {
      clearTimeout(timerRef.current)
    }

    timerRef.current = setTimeout(() => {
      setLoading(true)
      setError(null)
      Promise.all([
        getManifest(order.namespace, activePrepName),
        previewOrder(formData),
      ])
        .then(([activeMani, previewMani]) => {
          setBefore(activeMani)
          setAfter(previewMani)
          setLoading(false)
        })
        .catch((e: Error) => {
          setError(e.message)
          setLoading(false)
        })
    }, DEBOUNCE_MS)

    return () => {
      if (timerRef.current !== null) {
        clearTimeout(timerRef.current)
      }
    }
  }, [formData, order.namespace, activePrepName]) // eslint-disable-line react-hooks/exhaustive-deps

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
            <Btn variant="secondary" size="sm" onClick={() => setHideCRDs((v) => !v)}>
              {hideCRDs ? 'Show CRDs' : 'Hide CRDs'}
            </Btn>
          )}
          <Btn variant="secondary" size="sm" onClick={() => setShowFull((v) => !v)}>
            {showFull ? 'Show changed only' : 'Show full file'}
          </Btn>
        </div>
      </div>
      <DiffView lines={displayLines} />
    </div>
  )
}
