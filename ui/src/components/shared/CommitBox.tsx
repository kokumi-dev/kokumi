import { useState } from 'react'
import { Button } from '../ui'
import styles from './CommitBox.module.css'

interface Props {
  title?: string
  submitLabel?: string
  onCommit: (message: string) => Promise<void>
  onCancel?: () => void
  /** Disables submitting, e.g. while there are no changes. */
  disabled?: boolean
  disabledReason?: string
}

/** CommitBox is the inline "commit changes" panel shown at the end of an edit flow. */
export default function CommitBox({
  title = 'Commit changes',
  submitLabel = 'Commit changes',
  onCommit,
  onCancel,
  disabled,
  disabledReason,
}: Props) {
  const [message, setMessage] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function handleCommit() {
    setSaving(true)
    setError(null)
    try {
      await onCommit(message.trim())
      setMessage('')
    } catch (e) {
      setError((e as Error).message || 'Failed to save changes')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className={styles.box}>
      <label className={styles.title} htmlFor="commit-box-message">{title}</label>
      <textarea
        id="commit-box-message"
        className={styles.textarea}
        rows={3}
        value={message}
        onChange={(e) => setMessage(e.target.value)}
        placeholder="Describe why you are making this change (optional)"
        disabled={saving}
      />
      {error && <p className={styles.error}>{error}</p>}
      <div className={styles.actions}>
        {disabled && disabledReason && <span className={styles.hint}>{disabledReason}</span>}
        {onCancel && (
          <Button variant="secondary" onClick={onCancel} disabled={saving}>Cancel</Button>
        )}
        <Button variant="primary" onClick={handleCommit} disabled={saving || disabled}>
          {saving ? 'Saving…' : submitLabel}
        </Button>
      </div>
    </div>
  )
}
