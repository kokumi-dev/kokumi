import type { ReactNode } from 'react'
import styles from './layout.module.css'

interface Props {
  text: ReactNode
  icon?: ReactNode
  action?: ReactNode
}

export default function EmptyState({ text, icon, action }: Props) {
  return (
    <div className={styles.empty}>
      {icon}
      <span className={styles.emptyText}>{text}</span>
      {action}
    </div>
  )
}
