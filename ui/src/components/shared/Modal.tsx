import { createPortal } from 'react-dom'
import type { ReactNode } from 'react'
import { CloseIcon, IconButton } from '../ui'
import styles from './Modal.module.css'

interface Props {
  title: string
  onClose: () => void
  children: ReactNode
  footer?: ReactNode
  /** Apply a wider max-width variant */
  wide?: boolean
}

/**
 * Modal renders as a fixed overlay portal with a header (title + close) and
 * optional footer. The body slot scrolls independently.
 */
export default function Modal({ title, onClose, children, footer, wide }: Props) {
  return createPortal(
    <div
      className={styles.overlay}
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose()
      }}
    >
      <div className={`${styles.modal} ${wide ? styles.modalWide : ''}`}>
        <div className={styles.header}>
          <span className={styles.title}>{title}</span>
          <IconButton onClick={onClose} aria-label="Close">
            <CloseIcon />
          </IconButton>
        </div>

        <div className={styles.body}>{children}</div>

        {footer && <div className={styles.footer}>{footer}</div>}
      </div>
    </div>,
    document.body,
  )
}
