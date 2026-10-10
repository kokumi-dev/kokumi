import { useEffect, useState } from 'react'
import { getManifest, getManifestFiles, saveOrderEdits } from '../../api/client'
import type { ArtifactFile, Order, Patch, Preparation } from '../../api/types'
import { computeEdits } from '../../utils/edits'
import { filterCRDDocuments, hasCRDDocuments } from '../../utils/manifest'
import { Button, CopyIcon, EditIcon } from '../ui'
import YamlEditor from '../shared/YamlEditor'
import FileInspector from '../shared/FileInspector'
import CommitBox from '../shared/CommitBox'
import layout from '../layout/layout.module.css'
import styles from './ManifestPanel.module.css'

interface Props {
  preparation: Preparation
  /** When provided, the manifest is editable and edits are saved to this order. */
  order?: Order
}

/**
 * ManifestPanel shows the rendered YAML of a Preparation. When an editable
 * Order is given, changes are turned into structured patches on spec.edits.
 */
export default function ManifestPanel({ preparation: prep, order }: Props) {
  const [content, setContent] = useState<string | null>(null)
  const [files, setFiles] = useState<ArtifactFile[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [hideCRDs, setHideCRDs] = useState(true)
  const [editing, setEditing] = useState(false)
  const [editedContent, setEditedContent] = useState('')
  const [pendingEdits, setPendingEdits] = useState<Patch[] | null>(null)

  useEffect(() => {
    let cancelled = false
    getManifest(prep.namespace, prep.name)
      .then((c) => !cancelled && setContent(c))
      .catch((e: Error) => !cancelled && setError(e.message))
    getManifestFiles(prep.namespace, prep.name)
      .then((f) => !cancelled && setFiles(f))
      .catch(() => !cancelled && setFiles(null))
    return () => { cancelled = true }
  }, [prep.namespace, prep.name])

  const multiFile = files !== null && files.length > 1
  const hasCRDs = content !== null && hasCRDDocuments(content)
  const displayContent = content !== null ? filterCRDDocuments(content, hideCRDs) : null
  const canEdit = !!order
  const editCount = (order?.edits ?? []).reduce((n, e) => n + Object.keys(e.set).length, 0)

  function startEdit() {
    if (displayContent === null) return
    setEditedContent(displayContent)
    setEditing(true)
  }

  function discard() {
    setEditing(false)
    setEditedContent('')
    setPendingEdits(null)
  }

  function reviewEdits() {
    if (!order || displayContent === null) return
    setPendingEdits(computeEdits(displayContent, editedContent, order.edits ?? []))
  }

  function handleFileSave(editedFiles: ArtifactFile[]) {
    if (!order || content === null) return
    const joined = editedFiles.map((f) => f.content).join('\n---\n')
    setPendingEdits(computeEdits(content, joined, order.edits ?? []))
  }

  async function commit(message: string) {
    if (!order || pendingEdits === null) return
    await saveOrderEdits(order.namespace, order.name, pendingEdits, message)
    discard()
  }

  function copyToClipboard() {
    const text = editing ? editedContent : displayContent
    if (text) void navigator.clipboard.writeText(text)
  }

  if (error) return <p className={layout.error}>Failed to load manifest: {error}</p>
  if (displayContent === null) return <p className={layout.muted}>Loading…</p>

  return (
    <div className={layout.stack}>
      <div className={styles.toolbar}>
        <span className={layout.muted} style={{ fontSize: '0.82rem' }}>
          {editing ? 'Editing manifest' : `Rendered by ${prep.name}`}
          {editCount > 0 && <span className={styles.editsPill}>{editCount} edit(s) applied</span>}
        </span>
        <div className={styles.toolbarActions}>
          {hasCRDs && !editing && !multiFile && (
            <Button variant="secondary" size="sm" onClick={() => setHideCRDs((v) => !v)}>
              {hideCRDs ? 'Show CRDs' : 'Hide CRDs'}
            </Button>
          )}
          <Button variant="secondary" size="sm" icon={<CopyIcon />} onClick={copyToClipboard}>Copy</Button>
          {canEdit && !multiFile && !editing && (
            <Button variant="secondary" size="sm" icon={<EditIcon />} onClick={startEdit}>Edit</Button>
          )}
          {editing && pendingEdits === null && (
            <>
              <Button variant="secondary" size="sm" onClick={discard}>Discard</Button>
              <Button
                variant="primary"
                size="sm"
                onClick={reviewEdits}
                disabled={editedContent === displayContent}
              >
                Save edits…
              </Button>
            </>
          )}
        </div>
      </div>

      {multiFile && files ? (
        <FileInspector files={files} editable={canEdit} onSave={handleFileSave} />
      ) : editing ? (
        <YamlEditor value={editedContent} onChange={setEditedContent} tall />
      ) : (
        <YamlEditor value={displayContent} readOnly tall />
      )}

      {pendingEdits !== null && (
        <CommitBox
          title={`Commit edits to ${order?.name}`}
          onCommit={commit}
          onCancel={() => setPendingEdits(null)}
        />
      )}
    </div>
  )
}
