import type { ButtonHTMLAttributes, ReactNode } from 'react'
import { Link, type LinkProps } from 'react-router'
import styles from './Btn.module.css'

type Variant = 'primary' | 'secondary' | 'danger' | 'ghost' | 'promote' | 'rollback'
type Size = 'default' | 'sm'

interface Props extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant
  size?: Size
  children: ReactNode
}

function btnClass(variant: Variant, size: Size, className?: string): string {
  return [styles.btn, styles[variant], size === 'sm' ? styles.sm : '', className ?? '']
    .filter(Boolean)
    .join(' ')
}

export default function Btn({
  variant = 'secondary',
  size = 'default',
  className,
  children,
  ...rest
}: Props) {
  return (
    <button className={btnClass(variant, size, className)} {...rest}>
      {children}
    </button>
  )
}

interface LinkBtnProps extends LinkProps {
  variant?: Variant
  size?: Size
}

/** A router link styled as a button, for navigation actions. */
export function BtnLink({ variant = 'secondary', size = 'default', className, ...rest }: LinkBtnProps) {
  return <Link className={btnClass(variant, size, className)} {...rest} />
}
