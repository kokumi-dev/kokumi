import { createPortal } from 'react-dom'
import type { ReactNode } from 'react'
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
          <button className={styles.closeBtn} onClick={onClose} aria-label="Close">
            <svg viewBox="0 0 14 14" width="14" height="14" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
              <path d="M2 2l10 10M12 2L2 12" />
            </svg>
          </button>
        </div>

        <div className={styles.body}>{children}</div>

        {footer && <div className={styles.footer}>{footer}</div>}
      </div>
    </div>,
    document.body,
  )
}
