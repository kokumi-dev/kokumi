import { useEffect, useState } from 'react'
import type { ArtifactFile } from '../../api/types'
import { filterCRDDocuments, hasCRDDocuments } from '../../utils/manifest'
import { Button } from '../ui'
import YamlEditor from './YamlEditor'
import FileInspector from './FileInspector'
import styles from './PreviewTab.module.css'

interface Props<T> {
  /** What to render; null while too little is filled in to render anything. */
  request: T | null
  renderManifest: (request: T) => Promise<string>
  renderFiles: (request: T) => Promise<ArtifactFile[]>
  /** Shown while request is null. */
  placeholder?: string
}

const DEBOUNCE_MS = 600

/**
 * Renders a preview request on the server and shows the resulting manifest,
 * or a file browser when the result has several files. Nothing is pushed.
 */
export default function PreviewTab<T>({ request, renderManifest, renderFiles, placeholder }: Props<T>) {
  const [loading, setLoading] = useState(false)
  const [content, setContent] = useState<string | null>(null)
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
      renderManifest(req)
        .then((text) => {
          if (cancelled) return
          setContent(text)
          setLoading(false)
        })
        .catch((e: Error) => {
          if (cancelled) return
          setError(e.message)
          setLoading(false)
        })
      renderFiles(req)
        .then((f) => { if (!cancelled) setFiles(f) })
        .catch(() => { if (!cancelled) setFiles(null) })
    }, DEBOUNCE_MS)

    return () => {
      cancelled = true
      clearTimeout(timer)
    }
  }, [key, renderManifest, renderFiles])

  const multiFile = files !== null && files.length > 1

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

  if (content === null) {
    return null
  }

  const hasCRDs = hasCRDDocuments(content)
  const displayed = filterCRDDocuments(content, hideCRDs)

  return (
    <div>
      {hasCRDs && !multiFile && (
        <div className={styles.toolbar}>
          <Button variant="secondary" size="sm" onClick={() => setHideCRDs((v) => !v)}>
            {hideCRDs ? 'Show CRDs' : 'Hide CRDs'}
          </Button>
        </div>
      )}
      {multiFile && files ? (
        <FileInspector files={files} />
      ) : (
        <YamlEditor value={displayed} readOnly tall />
      )}
    </div>
  )
}
