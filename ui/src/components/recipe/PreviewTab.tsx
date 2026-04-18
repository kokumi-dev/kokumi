import { useEffect, useRef, useState } from 'react'
import { previewOrder } from '../../api/client'
import type { OrderFormData } from '../../api/types'
import { filterCRDDocuments, hasCRDDocuments } from '../../utils/manifest'
import Btn from '../shared/Btn'
import YamlEditor from '../shared/YamlEditor'
import styles from './PreviewTab.module.css'

interface Props {
  formData: OrderFormData
}

const DEBOUNCE_MS = 600

export default function PreviewTab({ formData }: Props) {
  const [loading, setLoading] = useState(false)
  const [content, setContent] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [hideCRDs, setHideCRDs] = useState(true)

  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  const hasSource = !!(formData.source?.oci || formData.menuRef)

  useEffect(() => {
    if (timerRef.current !== null) {
      clearTimeout(timerRef.current)
    }

    if (!hasSource) {
      return
    }

    timerRef.current = setTimeout(() => {
      setLoading(true)
      setError(null)
      previewOrder(formData)
        .then((text) => {
          setContent(text)
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
  }, [formData]) // eslint-disable-line react-hooks/exhaustive-deps

  if (!hasSource) {
    return (
      <div className={styles.placeholder}>
        Fill in source details to preview the rendered manifest.
      </div>
    )
  }

  if (loading) {
    return (
      <div className={styles.placeholder}>
        Loading preview…
      </div>
    )
  }

  if (error) {
    return <p className={styles.error}>Failed to load preview: {error}</p>
  }

  if (content === null) {
    return null
  }

  const hasCRDs = hasCRDDocuments(content)
  const displayed = filterCRDDocuments(content, hideCRDs)

  return (
    <div>
      {hasCRDs && (
        <div className={styles.toolbar}>
          <Btn variant="secondary" size="sm" onClick={() => setHideCRDs((v) => !v)}>
            {hideCRDs ? 'Show CRDs' : 'Hide CRDs'}
          </Btn>
        </div>
      )}
      <YamlEditor value={displayed} readOnly tall />
    </div>
  )
}
