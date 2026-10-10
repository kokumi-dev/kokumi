import { useEffect, useMemo, useState } from 'react'
import { getManifest, getManifestFiles } from '../../api/client'
import type { ArtifactFile, Preparation } from '../../api/types'
import { computeDiff, filterContext } from '../../utils/diff'
import { filterCRDDocuments, hasCRDDocuments } from '../../utils/manifest'
import Btn from '../shared/Btn'
import DiffView from '../shared/DiffView'
import layout from '../layout/layout.module.css'
import styles from './PreparationDiff.module.css'

interface Props {
  base: Preparation
  target: Preparation
}

const CONTEXT_SIZE = 5

type State =
  | { status: 'loading' }
  | { status: 'error'; message: string }
  | { status: 'ready'; before: string; after: string; beforeFiles: ArtifactFile[] | null; afterFiles: ArtifactFile[] | null }

/**
 * PreparationDiff renders a git-style diff of two Preparations' manifests.
 * Give it a key per base/target pair so a new pair starts from loading.
 */
export default function PreparationDiff({ base, target }: Props) {
  const [state, setState] = useState<State>({ status: 'loading' })
  const [showFull, setShowFull] = useState(false)
  const [hideCRDs, setHideCRDs] = useState(true)

  useEffect(() => {
    let cancelled = false
    Promise.all([
      getManifest(base.namespace, base.name),
      getManifest(target.namespace, target.name),
      getManifestFiles(base.namespace, base.name).catch(() => null),
      getManifestFiles(target.namespace, target.name).catch(() => null),
    ])
      .then(([before, after, beforeFiles, afterFiles]) => {
        if (!cancelled) setState({ status: 'ready', before, after, beforeFiles, afterFiles })
      })
      .catch((e: Error) => !cancelled && setState({ status: 'error', message: e.message }))
    return () => { cancelled = true }
  }, [base.namespace, base.name, target.namespace, target.name])

  const current = state

  const fileDiffs = useMemo(() => {
    if (current.status !== 'ready' || !current.beforeFiles || !current.afterFiles) return null
    if (current.beforeFiles.length <= 1 && current.afterFiles.length <= 1) return null
    const beforeMap = new Map(current.beforeFiles.map((f) => [f.path, f.content]))
    const afterMap = new Map(current.afterFiles.map((f) => [f.path, f.content]))
    const all = [...new Set([...beforeMap.keys(), ...afterMap.keys()])].sort()
    return all.map((path) => ({ path, lines: computeDiff(beforeMap.get(path) ?? '', afterMap.get(path) ?? '') }))
  }, [current])

  const allLines = useMemo(() => {
    if (current.status !== 'ready') return []
    return computeDiff(filterCRDDocuments(current.before, hideCRDs), filterCRDDocuments(current.after, hideCRDs))
  }, [current, hideCRDs])

  if (current.status === 'loading') return <p className={layout.muted}>Loading manifests…</p>
  if (current.status === 'error') return <p className={layout.error}>Failed to load manifests: {current.message}</p>

  const hasCRDs = hasCRDDocuments(current.before) || hasCRDDocuments(current.after)
  const context = showFull ? Infinity : CONTEXT_SIZE

  return (
    <>
      <div className={styles.toolbar}>
        <span className={styles.toolbarLabel}>
          {base.name} → {target.name}
        </span>
        <div style={{ display: 'flex', gap: '8px' }}>
          {hasCRDs && !fileDiffs && (
            <Btn variant="secondary" size="sm" onClick={() => setHideCRDs((v) => !v)}>
              {hideCRDs ? 'Show CRDs' : 'Hide CRDs'}
            </Btn>
          )}
          <Btn variant="secondary" size="sm" onClick={() => setShowFull((v) => !v)}>
            {showFull ? 'Show changed only' : 'Show full file'}
          </Btn>
        </div>
      </div>

      {fileDiffs ? (
        <div style={{ display: 'flex', flexDirection: 'column', gap: '16px' }}>
          {fileDiffs.map(({ path, lines }) => (
            <div key={path}>
              <div className={styles.fileHeader}>{path}</div>
              <DiffView lines={filterContext(lines, context)} />
            </div>
          ))}
        </div>
      ) : (
        <DiffView lines={filterContext(allLines, context)} />
      )}
    </>
  )
}
