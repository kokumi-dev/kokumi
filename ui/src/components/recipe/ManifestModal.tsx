import { useEffect, useState, useCallback } from 'react'
import { getManifest, saveOrderEdits } from '../../api/client'
import type { Order, Preparation } from '../../api/types'
import { computeEdits } from '../../utils/edits'
import { filterCRDDocuments, hasCRDDocuments } from '../../utils/manifest'
import Modal from '../shared/Modal'
import Btn from '../shared/Btn'
import YamlEditor from '../shared/YamlEditor'
import CommitMessageModal from './CommitMessageModal'

interface Props {
  preparation: Preparation
  /** When provided, the manifest is editable and edits are saved to this order. */
  order?: Order
  onClose: () => void
}

/**
 * ManifestModal fetches the rendered Kubernetes YAML for a Preparation from
 * the OCI artifact and displays it in a CodeMirror editor. When the order has
 * no menuRef or the menu allows edits, the manifest is editable. Changes are
 * computed as structured patches and saved to the Order's spec.edits field.
 */
export default function ManifestModal({ preparation: prep, order, onClose }: Props) {
  const [content, setContent] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [hideCRDs, setHideCRDs] = useState(true)
  const [editing, setEditing] = useState(false)
  const [editedContent, setEditedContent] = useState('')
  const [saving, setSaving] = useState(false)
  const [showCommitModal, setShowCommitModal] = useState(false)
  const [pendingEdits, setPendingEdits] = useState<ReturnType<typeof computeEdits> | null>(null)

  useEffect(() => {
    getManifest(prep.namespace, prep.name)
      .then(setContent)
      .catch((e: Error) => setError(e.message))
  }, [prep.namespace, prep.name])

  const hasCRDs = content !== null && hasCRDDocuments(content)
  const displayContent = content !== null ? filterCRDDocuments(content, hideCRDs) : null

  const canEdit = !!order

  const handleStartEdit = useCallback(() => {
    if (displayContent) {
      setEditedContent(displayContent)
      setEditing(true)
    }
  }, [displayContent])

  const handleDiscard = useCallback(() => {
    setEditing(false)
    setEditedContent('')
  }, [])

  const handleSave = useCallback(async () => {
    if (!content || !displayContent || !order) return
    const edits = computeEdits(displayContent, editedContent, order.edits ?? [])
    setPendingEdits(edits)
    setShowCommitModal(true)
  }, [content, displayContent, editedContent, order])

  const handleCommit = useCallback(async (commitMessage: string) => {
    if (!order || pendingEdits === null) return
    setSaving(true)
    try {
      await saveOrderEdits(order.namespace, order.name, pendingEdits, commitMessage)
      setEditing(false)
      setEditedContent('')
      setShowCommitModal(false)
      setPendingEdits(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to save edits')
    } finally {
      setSaving(false)
    }
  }, [order, pendingEdits])

  function copyToClipboard() {
    const text = editing ? editedContent : displayContent
    if (text) void navigator.clipboard.writeText(text)
  }

  const hasEdits = (order?.edits ?? []).length > 0

  const footer = (
    <>
      {displayContent && !editing && canEdit && (
        <Btn variant="primary" size="sm" onClick={handleStartEdit}>
          Edit
        </Btn>
      )}
      {editing && (
        <>
          <Btn variant="primary" size="sm" onClick={handleSave} disabled={saving}>
            {saving ? 'Saving…' : 'Save Edits'}
          </Btn>
          <Btn variant="secondary" size="sm" onClick={handleDiscard} disabled={saving}>
            Discard
          </Btn>
        </>
      )}
      {displayContent && (
        <Btn variant="secondary" size="sm" onClick={copyToClipboard}>
          Copy
        </Btn>
      )}
      <Btn variant="secondary" size="sm" onClick={onClose}>
        Close
      </Btn>
    </>
  )

  return (
    <>
      <Modal
        title={`Manifest — ${prep.name}${editing ? ' (editing)' : ''}`}
        onClose={onClose}
        footer={footer}
        wide
      >
        {error ? (
          <p style={{ color: '#c0312e', fontSize: '0.875rem' }}>
            Failed to load manifest: {error}
          </p>
        ) : displayContent === null ? (
          <p style={{ color: 'var(--color-text-muted-light)', fontSize: '0.875rem' }}>
            Loading…
          </p>
        ) : (
          <>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '12px' }}>
              <div style={{ display: 'flex', gap: '8px', alignItems: 'center' }}>
                {hasEdits && (
                  <span style={{
                    fontSize: '0.75rem',
                    color: 'var(--color-accent)',
                    background: 'var(--color-accent-dim, rgba(99, 102, 241, 0.1))',
                    padding: '2px 8px',
                    borderRadius: '4px',
                  }}>
                    {order!.edits!.reduce((n, e) => n + Object.keys(e.set).length, 0)} edit(s) applied
                  </span>
                )}
              </div>
              {hasCRDs && !editing && (
                <Btn variant="secondary" size="sm" onClick={() => setHideCRDs((v) => !v)}>
                  {hideCRDs ? 'Show CRDs' : 'Hide CRDs'}
                </Btn>
              )}
            </div>
            {editing ? (
              <YamlEditor value={editedContent} onChange={setEditedContent} tall />
            ) : (
              <YamlEditor value={displayContent} readOnly tall />
            )}
          </>
        )}
      </Modal>

      {showCommitModal && (
        <CommitMessageModal
          onClose={() => { setShowCommitModal(false); setPendingEdits(null) }}
          onCommit={handleCommit}
          loading={saving}
        />
      )}
    </>
  )
}
