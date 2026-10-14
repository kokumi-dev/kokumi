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
  /**
   * Marks the action unavailable and explains why in a tooltip. Unlike
   * `disabled`, the control stays focusable so keyboard and screen-reader
   * users can discover the reason.
   */
  disabledReason?: string
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
  disabledReason,
  className,
  type = 'button',
  title,
  onClick,
  disabled,
  children,
  ...rest
}: ButtonProps) {
  return (
    <button
      type={type}
      className={buttonClass(variant, size, block, className)}
      // disabledReason takes over from disabled so the control stays focusable.
      disabled={disabledReason ? undefined : disabled}
      aria-disabled={disabledReason ? true : undefined}
      title={disabledReason ?? title}
      onClick={disabledReason ? (e) => e.preventDefault() : onClick}
      {...rest}
    >
      {icon && <span className={styles.icon}>{icon}</span>}
      {children}
    </button>
  )
}

interface ButtonLinkProps extends CommonProps, LinkProps {}

/** A router link styled as a button, for navigation actions. */
export function ButtonLink({ variant = 'secondary', size = 'md', icon, block, disabledReason, className, children, ...rest }: ButtonLinkProps) {
  const content = (
    <>
      {icon && <span className={styles.icon}>{icon}</span>}
      {children}
    </>
  )
  if (disabledReason) {
    // Without an href the link cannot navigate; role and tabIndex keep it
    // announced and focusable as an unavailable link.
    return (
      <a role="link" aria-disabled="true" tabIndex={0} title={disabledReason} className={buttonClass(variant, size, block, className)}>
        {content}
      </a>
    )
  }
  return (
    <Link className={buttonClass(variant, size, block, className)} {...rest}>
      {content}
    </Link>
  )
}
