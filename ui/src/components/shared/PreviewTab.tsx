import { useEffect, useState } from 'react'
import type { ArtifactFile } from '../../api/types'
import { filterCRDDocuments, hasCRDDocuments, joinFiles } from '../../utils/manifest'
import { Button } from '../ui'
import YamlEditor from './YamlEditor'
import FileInspector from './FileInspector'
import styles from './PreviewTab.module.css'

interface Props<T> {
  /** What to render; null while too little is filled in to render anything. */
  request: T | null
  /** Renders the request into the artifact's files. */
  render: (request: T) => Promise<ArtifactFile[]>
  /** Shown while request is null. */
  placeholder?: string
}

const DEBOUNCE_MS = 600

/**
 * Renders a preview request on the server and shows the resulting manifest,
 * or a file browser when the result has several files. Nothing is pushed.
 */
export default function PreviewTab<T>({ request, render, placeholder }: Props<T>) {
  const [loading, setLoading] = useState(false)
  const [files, setFiles] = useState<ArtifactFile[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [hideCRDs, setHideCRDs] = useState(true)

  // Keyed by content, so callers may build a fresh request on every render.
  const key = request === null ? '' : JSON.stringify(request)

  useEffect(() => {
    if (!key) return
    const req = JSON.parse(key) as T
    let cancelled = false
    const timer = setTimeout(() => {
      setLoading(true)
      setError(null)
      render(req)
        .then((f) => {
          if (cancelled) return
          setFiles(f)
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
  }, [key, render])

  if (!key) {
    return <div className={styles.placeholder}>{placeholder}</div>
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

  if (files === null) {
    return null
  }

  if (files.length > 1) {
    return <FileInspector files={files} />
  }

  const content = joinFiles(files)

  return (
    <div>
      {hasCRDDocuments(content) && (
        <div className={styles.toolbar}>
          <Button variant="secondary" size="sm" onClick={() => setHideCRDs((v) => !v)}>
            {hideCRDs ? 'Show CRDs' : 'Hide CRDs'}
          </Button>
        </div>
      )}
      <YamlEditor value={filterCRDDocuments(content, hideCRDs)} readOnly tall />
    </div>
  )
}
