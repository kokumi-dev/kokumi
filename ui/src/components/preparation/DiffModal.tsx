import { useEffect, useMemo, useState } from 'react'
import { getManifest, getManifestFiles } from '../../api/client'
import type { ArtifactFile, Preparation } from '../../api/types'
import { computeDiff, filterContext } from '../../utils/diff'
import { filterCRDDocuments, hasCRDDocuments } from '../../utils/manifest'
import Modal from '../shared/Modal'
import Btn from '../shared/Btn'
import DiffView from '../shared/DiffView'
import styles from './DiffModal.module.css'

interface Props {
  /** The Preparation being examined (non-active). */
  preparation: Preparation
  /** The currently active Preparation to diff against. */
  activePreparation: Preparation
  onClose: () => void
}

const CONTEXT_SIZE = 5

/**
 * DiffModal fetches the manifests for two Preparations, runs a line diff, and
 * renders a git-style diff view. A toggle switches between "changed only
 * (±5 context lines)" and "full file" modes.
 */
export default function DiffModal({ preparation, activePreparation, onClose }: Props) {
  const [state, setState] = useState<
    | { status: 'loading' }
    | { status: 'error'; message: string }
    | { status: 'ready'; before: string; after: string; beforeFiles: ArtifactFile[] | null; afterFiles: ArtifactFile[] | null }
  >({ status: 'loading' })

  const [showFull, setShowFull] = useState(false)
  const [hideCRDs, setHideCRDs] = useState(true)

  useEffect(() => {
    Promise.all([
      getManifest(activePreparation.namespace, activePreparation.name),
      getManifest(preparation.namespace, preparation.name),
      getManifestFiles(activePreparation.namespace, activePreparation.name).catch(() => null),
      getManifestFiles(preparation.namespace, preparation.name).catch(() => null),
    ])
      .then(([before, after, beforeFiles, afterFiles]) => {
        setState({ status: 'ready', before, after, beforeFiles, afterFiles })
      })
      .catch((e: Error) => setState({ status: 'error', message: e.message }))
  }, [preparation.namespace, preparation.name, activePreparation.namespace, activePreparation.name])

  const hasCRDs =
    state.status === 'ready' &&
    (hasCRDDocuments(state.before) || hasCRDDocuments(state.after))

  const fileDiffs = useMemo(() => {
    if (state.status !== 'ready' || !state.beforeFiles || !state.afterFiles) return null
    if (state.beforeFiles.length <= 1 && state.afterFiles.length <= 1) return null

    const beforeMap = new Map(state.beforeFiles.map((f) => [f.path, f.content]))
    const paths = [...new Set([...state.beforeFiles.map((f) => f.path), ...state.afterFiles.map((f) => f.path)])].sort()

    return paths.map((path) => {
      const before = beforeMap.get(path) ?? ''
      const after = state.afterFiles!.find((f) => f.path === path)?.content ?? ''
      return { path, lines: computeDiff(before, after) }
    })
  }, [state])

  const allLines = useMemo(() => {
    if (state.status !== 'ready') return []
    const before = filterCRDDocuments(state.before, hideCRDs)
    const after = filterCRDDocuments(state.after, hideCRDs)
    return computeDiff(before, after)
  }, [state, hideCRDs])

  const displayLines = filterContext(allLines, showFull ? Infinity : CONTEXT_SIZE)

  const footer = (
    <Btn variant="secondary" onClick={onClose}>
      Close
    </Btn>
  )

  return (
    <Modal
      title={`Diff — ${activePreparation.name} → ${preparation.name}`}
      onClose={onClose}
      footer={footer}
      wide
    >
      {state.status === 'loading' && (
        <p style={{ color: 'var(--color-text-muted-light)', fontSize: '0.875rem' }}>
          Loading manifests…
        </p>
      )}

      {state.status === 'error' && (
        <p style={{ color: '#c0312e', fontSize: '0.875rem' }}>
          Failed to load manifests: {state.message}
        </p>
      )}

      {state.status === 'ready' && (
        <>
          <div className={styles.toolbar}>
            <span className={styles.toolbarLabel}>
              {activePreparation.name} (active) → {preparation.name}
            </span>
            <div style={{ display: 'flex', gap: '8px' }}>
              {hasCRDs && (
                <Btn
                  variant="secondary"
                  size="sm"
                  onClick={() => setHideCRDs((v) => !v)}
                >
                  {hideCRDs ? 'Show CRDs' : 'Hide CRDs'}
                </Btn>
              )}
              <Btn
                variant="secondary"
                size="sm"
                onClick={() => setShowFull((v) => !v)}
              >
                {showFull ? 'Show changed only' : 'Show full file'}
              </Btn>
            </div>
          </div>

          {fileDiffs ? (
            <div style={{ display: 'flex', flexDirection: 'column', gap: '16px' }}>
              {fileDiffs.map(({ path, lines }) => (
                <div key={path}>
                  <div className={styles.fileHeader}>{path}</div>
                  <DiffView lines={filterContext(lines, showFull ? Infinity : CONTEXT_SIZE)} />
                </div>
              ))}
            </div>
          ) : (
            <DiffView lines={displayLines} />
          )}
        </>
      )}
    </Modal>
  )
}
