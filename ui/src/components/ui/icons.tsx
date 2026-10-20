import type { SVGProps } from 'react'

type IconProps = SVGProps<SVGSVGElement> & { size?: number }

function Svg({ size = 14, children, ...rest }: IconProps) {
  return (
    <svg
      viewBox="0 0 16 16"
      width={size}
      height={size}
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
      {...rest}
    >
      {children}
    </svg>
  )
}

export function PlusIcon(p: IconProps) {
  return <Svg {...p}><path d="M8 3v10M3 8h10" /></Svg>
}

export function CloseIcon(p: IconProps) {
  return <Svg {...p}><path d="M4 4l8 8M12 4l-8 8" /></Svg>
}

export function TrashIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <path d="M3 4.5h10M6.5 4.5V3h3v1.5M4.5 4.5l.6 8.5h5.8l.6-8.5" />
    </Svg>
  )
}

export function EditIcon(p: IconProps) {
  return <Svg {...p}><path d="M10.5 2.5l3 3L6 13H3v-3z" /></Svg>
}

export function PromoteIcon(p: IconProps) {
  return <Svg {...p}><path d="M8 13V3M4 7l4-4 4 4" /></Svg>
}

export function RollbackIcon(p: IconProps) {
  return <Svg {...p}><path d="M3 6h7a3 3 0 010 6H7M6 3L3 6l3 3" /></Svg>
}

/** Review: look at a Preparation before approving it or requesting changes. */
export function EyeIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <path d="M1.5 8S4 3.5 8 3.5 14.5 8 14.5 8 12 12.5 8 12.5 1.5 8 1.5 8z" />
      <circle cx="8" cy="8" r="2" />
    </Svg>
  )
}

export function DownloadIcon(p: IconProps) {
  return <Svg {...p}><path d="M8 2.5v8M4.5 7L8 10.5 11.5 7M3 13.5h10" /></Svg>
}

/** Security: vulnerability scans and checks. */
export function ShieldIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <path d="M8 1.75L13 3.5v4c0 3.1-2.1 5.6-5 6.75C5.1 13.1 3 10.6 3 7.5v-4z" />
      <path d="M5.75 8l1.5 1.5 3-3" />
    </Svg>
  )
}

export function CopyIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <rect x="5.5" y="5.5" width="8" height="8" rx="1.5" />
      <path d="M10.5 5.5V3.5a1 1 0 00-1-1h-6a1 1 0 00-1 1v6a1 1 0 001 1h2" />
    </Svg>
  )
}
