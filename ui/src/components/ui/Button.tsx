import type { ButtonHTMLAttributes, ReactNode } from 'react'
import { Link, type LinkProps } from 'react-router'
import styles from './Button.module.css'

type ButtonVariant = 'primary' | 'secondary' | 'danger' | 'ghost' | 'promote' | 'rollback' | 'inverse'
/** md for page headers, dialog and form footers; sm for rows and toolbars. */
type ButtonSize = 'md' | 'sm'

interface CommonProps {
  variant?: ButtonVariant
  size?: ButtonSize
  icon?: ReactNode
  /** Stretch to the container width. */
  block?: boolean
}

interface ButtonProps extends CommonProps, ButtonHTMLAttributes<HTMLButtonElement> {
  children: ReactNode
}

function buttonClass(variant: ButtonVariant, size: ButtonSize, block?: boolean, className?: string): string {
  return [styles.button, styles[variant], styles[size], block ? styles.block : '', className ?? '']
    .filter(Boolean)
    .join(' ')
}

export function Button({
  variant = 'secondary',
  size = 'md',
  icon,
  block,
  className,
  type = 'button',
  children,
  ...rest
}: ButtonProps) {
  return (
    <button type={type} className={buttonClass(variant, size, block, className)} {...rest}>
      {icon && <span className={styles.icon}>{icon}</span>}
      {children}
    </button>
  )
}

interface ButtonLinkProps extends CommonProps, LinkProps {}

/** A router link styled as a button, for navigation actions. */
export function ButtonLink({ variant = 'secondary', size = 'md', icon, block, className, children, ...rest }: ButtonLinkProps) {
  return (
    <Link className={buttonClass(variant, size, block, className)} {...rest}>
      {icon && <span className={styles.icon}>{icon}</span>}
      {children}
    </Link>
  )
}
