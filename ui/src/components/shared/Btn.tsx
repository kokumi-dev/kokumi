import type { ButtonHTMLAttributes, ReactNode } from 'react'
import styles from './Btn.module.css'

type Variant = 'primary' | 'secondary' | 'danger' | 'ghost' | 'promote' | 'rollback'
type Size = 'default' | 'sm'

interface Props extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant
  size?: Size
  children: ReactNode
}

export default function Btn({
  variant = 'secondary',
  size = 'default',
  className,
  children,
  ...rest
}: Props) {
  const cls = [
    styles.btn,
    styles[variant],
    size === 'sm' ? styles.sm : '',
    className ?? '',
  ]
    .filter(Boolean)
    .join(' ')

  return (
    <button className={cls} {...rest}>
      {children}
    </button>
  )
}
