import { useEffect, useMemo, useState } from 'react'
import { getManifest } from '../../api/client'
import type { ArtifactFile, Preparation } from '../../api/types'
import { computeDiff, filterContext } from '../../utils/diff'
import { filterCRDDocuments, hasCRDDocuments, joinFiles } from '../../utils/manifest'
import { diffImages, type ImageChange } from '../../utils/images'
import { Button } from '../ui'
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
  | { status: 'ready'; before: ArtifactFile[]; after: ArtifactFile[] }

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
    ])
      .then(([before, after]) => {
        if (!cancelled) setState({ status: 'ready', before, after })
      })
      .catch((e: Error) => !cancelled && setState({ status: 'error', message: e.message }))
    return () => { cancelled = true }
  }, [base.namespace, base.name, target.namespace, target.name])

  const current = state

  const fileDiffs = useMemo(() => {
    if (current.status !== 'ready') return null
    if (current.before.length <= 1 && current.after.length <= 1) return null
    const beforeMap = new Map(current.before.map((f) => [f.path, f.content]))
    const afterMap = new Map(current.after.map((f) => [f.path, f.content]))
    const all = [...new Set([...beforeMap.keys(), ...afterMap.keys()])].sort()
    return all.map((path) => ({ path, lines: computeDiff(beforeMap.get(path) ?? '', afterMap.get(path) ?? '') }))
  }, [current])

  const joined = useMemo(() => {
    if (current.status !== 'ready') return null
    return { before: joinFiles(current.before), after: joinFiles(current.after) }
  }, [current])

  const allLines = useMemo(() => {
    if (joined === null) return []
    return computeDiff(filterCRDDocuments(joined.before, hideCRDs), filterCRDDocuments(joined.after, hideCRDs))
  }, [joined, hideCRDs])

  if (current.status === 'error') return <p className={layout.error}>Failed to load manifests: {current.message}</p>
  if (joined === null) return <p className={layout.muted}>Loading manifests…</p>

  const hasCRDs = hasCRDDocuments(joined.before) || hasCRDDocuments(joined.after)
  const context = showFull ? Infinity : CONTEXT_SIZE
  const imageChanges = diffImages(base.images, target.images)

  return (
    <>
      {imageChanges.length > 0 && <ImageChanges changes={imageChanges} />}

      <div className={styles.toolbar}>
        <span className={styles.toolbarLabel}>
          {base.name} → {target.name}
        </span>
        <div style={{ display: 'flex', gap: '8px' }}>
          {hasCRDs && !fileDiffs && (
            <Button variant="secondary" size="sm" onClick={() => setHideCRDs((v) => !v)}>
              {hideCRDs ? 'Show CRDs' : 'Hide CRDs'}
            </Button>
          )}
          <Button variant="secondary" size="sm" onClick={() => setShowFull((v) => !v)}>
            {showFull ? 'Show changed only' : 'Show full file'}
          </Button>
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

const changeLabels: Record<ImageChange['kind'], string> = {
  added: 'Added',
  removed: 'Removed',
  changed: 'Changed',
}

/** Where an image is pulled from: its vendored location, else its digest. */
function imageTarget(image?: { digest?: string; vendored?: string }): string {
  return image?.vendored ?? image?.digest ?? 'not resolved'
}

/**
 * ImageChanges lists the images that differ between the two Preparations,
 * including digests that changed without any change to the manifests.
 */
function ImageChanges({ changes }: { changes: ImageChange[] }) {
  return (
    <div className={styles.images}>
      <span className={styles.toolbarLabel}>Image changes</span>
      <ul className={styles.imageList}>
        {changes.map((c) => (
          <li key={c.ref} className={styles.imageChange}>
            <span className={`${styles.changeKind} ${styles[c.kind]}`}>{changeLabels[c.kind]}</span>
            <span className={styles.mono}>{c.ref}</span>
            <span className={`${styles.mono} ${layout.muted}`}>
              {c.kind === 'changed'
                ? `${imageTarget(c.before)} → ${imageTarget(c.after)}`
                : imageTarget(c.after ?? c.before)}
            </span>
          </li>
        ))}
      </ul>
    </div>
  )
}
