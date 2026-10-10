import type { ButtonHTMLAttributes, ReactNode } from 'react'
import styles from './IconButton.module.css'

interface IconButtonProps extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'aria-label'> {
  'aria-label': string
  size?: 'sm' | 'xs'
  /** danger turns red on hover, for remove actions. */
  tone?: 'neutral' | 'danger'
  children: ReactNode
}

export function IconButton({ size = 'sm', tone = 'neutral', className, ...rest }: IconButtonProps) {
  return (
    <button
      type="button"
      title={rest.title ?? rest['aria-label']}
      className={[styles.iconButton, styles[size], tone === 'danger' ? styles.danger : '', className ?? '']
        .filter(Boolean)
        .join(' ')}
      {...rest}
    />
  )
}
