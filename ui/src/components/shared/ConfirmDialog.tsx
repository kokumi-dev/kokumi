import { useState, type ReactNode } from 'react'
import Modal from './Modal'
import { Button } from '../ui'
import styles from './Modal.module.css'

interface Props {
  title: string
  children: ReactNode
  confirmLabel: string
  variant?: 'primary' | 'danger' | 'promote' | 'rollback'
  onConfirm: () => Promise<void>
  onCancel: () => void
}

/** ConfirmDialog asks before a consequential action and surfaces its error inline. */
export default function ConfirmDialog({ title, children, confirmLabel, variant = 'primary', onConfirm, onCancel }: Props) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function handleConfirm() {
    setBusy(true)
    setError(null)
    try {
      await onConfirm()
    } catch (e) {
      setError((e as Error).message)
      setBusy(false)
    }
  }

  const footer = (
    <>
      <Button variant="secondary" onClick={onCancel} disabled={busy}>Cancel</Button>
      <Button variant={variant} onClick={handleConfirm} disabled={busy} autoFocus>
        {busy ? '…' : confirmLabel}
      </Button>
    </>
  )

  return (
    <Modal title={title} onClose={busy ? () => {} : onCancel} footer={footer}>
      <div style={{ fontSize: '0.9rem' }}>{children}</div>
      {error && <p className={styles.errorText}>{error}</p>}
    </Modal>
  )
}
