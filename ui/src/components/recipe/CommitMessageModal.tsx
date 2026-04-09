import { useState } from 'react'
import Modal from '../shared/Modal'
import Btn from '../shared/Btn'
import styles from './CommitMessageModal.module.css'

interface Props {
  onClose: () => void
  onCommit: (message: string) => void
  loading?: boolean
}

/**
 * CommitMessageModal prompts the user for an optional commit message before
 * saving changes to an Order, analogous to a git commit modal in GitLab/GitHub.
 */
export default function CommitMessageModal({ onClose, onCommit, loading }: Props) {
  const [message, setMessage] = useState('')

  const footer = (
    <>
      <Btn variant="secondary" onClick={onClose} disabled={loading}>
        Cancel
      </Btn>
      <Btn variant="primary" onClick={() => onCommit(message)} disabled={loading}>
        {loading ? 'Saving…' : 'Commit changes'}
      </Btn>
    </>
  )

  return (
    <Modal title="Commit changes" onClose={onClose} footer={footer}>
      <div className={styles.body}>
        <label className={styles.label} htmlFor="commit-message-input">
          Commit message
        </label>
        <textarea
          id="commit-message-input"
          className={styles.textarea}
          rows={4}
          value={message}
          onChange={(e) => setMessage(e.target.value)}
          placeholder="Describe why you are making this change (optional)"
          disabled={loading}
        />
      </div>
    </Modal>
  )
}
