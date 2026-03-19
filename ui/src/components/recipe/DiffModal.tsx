import { useEffect, useMemo, useState } from 'react'
import { getManifest } from '../../api/client'
import type { Preparation } from '../../api/types'
import { computeDiff, filterContext } from '../../utils/diff'
import type { DiffLine } from '../../utils/diff'
import { filterCRDDocuments, hasCRDDocuments } from '../../utils/manifest'
import Modal from '../shared/Modal'
import Btn from '../shared/Btn'
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
    | { status: 'ready'; before: string; after: string }
  >({ status: 'loading' })

  const [showFull, setShowFull] = useState(false)
  const [hideCRDs, setHideCRDs] = useState(true)

  useEffect(() => {
    Promise.all([
      getManifest(activePreparation.namespace, activePreparation.name),
      getManifest(preparation.namespace, preparation.name),
    ])
      .then(([before, after]) => {
        setState({ status: 'ready', before, after })
      })
      .catch((e: Error) => setState({ status: 'error', message: e.message }))
  }, [preparation.namespace, preparation.name, activePreparation.namespace, activePreparation.name])

  const hasCRDs =
    state.status === 'ready' &&
    (hasCRDDocuments(state.before) || hasCRDDocuments(state.after))

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

          <DiffView lines={displayLines} />
        </>
      )}
    </Modal>
  )
}

// ── DiffView ──────────────────────────────────────────────────────────────────

function DiffView({ lines }: { lines: DiffLine[] }) {
  if (lines.length === 0) {
    return (
      <p style={{ textAlign: 'center', color: 'var(--color-text-muted-light)', padding: '24px 0', fontSize: '0.875rem' }}>
        No differences found.
      </p>
    )
  }

  return (
    <div className={styles.diffWrap}>
      <table className={styles.diffTable}>
        <tbody>
          {lines.map((line, i) => (
            <DiffRow key={i} line={line} />
          ))}
        </tbody>
      </table>
    </div>
  )
}

function DiffRow({ line }: { line: DiffLine }) {
  if (line.type === 'omitted') {
    return (
      <tr className={styles.lineOmitted}>
        <td colSpan={3}>{line.content}</td>
      </tr>
    )
  }

  const rowClass =
    line.type === 'added'
      ? styles.lineAdded
      : line.type === 'removed'
        ? styles.lineRemoved
        : ''

  const prefix =
    line.type === 'added' ? '+' : line.type === 'removed' ? '-' : ' '

  return (
    <tr className={rowClass}>
      <td className={styles.lineGutter}>{line.lineNoBefore ?? ''}</td>
      <td className={styles.lineGutter}>{line.lineNoAfter ?? ''}</td>
      <td className={styles.lineContent}>
        <span className={styles.linePrefix}>{prefix}</span>
        {line.content}
      </td>
    </tr>
  )
}
